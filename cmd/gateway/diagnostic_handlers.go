package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

func (s *Server) handleCreateAgentDiagnostic(w http.ResponseWriter, r *http.Request) {
	const msg = "AgentDiagnostic is deprecated. Use AgentRequest with a GovernedResource " +
		"that has soakMode: true. See docs/agent-graduation-ladder.md"
	writeJSON(w, http.StatusGone, map[string]string{"error": msg})
}

func (s *Server) handleGetAgentDiagnostic(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}

	name := r.PathValue("name")
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var diag v1alpha1.AgentDiagnostic
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &diag); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentDiagnostic not found")
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to get AgentDiagnostic: %v", err))
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":           diag.Name,
		"namespace":      diag.Namespace,
		"createdAt":      diag.CreationTimestamp.Time,
		"agentIdentity":  diag.Spec.AgentIdentity,
		"diagnosticType": diag.Spec.DiagnosticType,
		"correlationID":  diag.Spec.CorrelationID,
		"summary":        diag.Spec.Summary,
		"details":        diag.Spec.Details,
		"status":         diag.Status, // Added status so dashboard can read verdict
	})
}

func (s *Server) handlePatchAgentDiagnosticStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	sub := callerSubFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}
	if !requireRole(s.roles, roleReviewer, sub, callerGroupsFromCtx(r.Context()), w) {
		return
	}

	var body struct {
		Verdict      string `json:"verdict"`
		ReviewerNote string `json:"reviewerNote,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.Verdict != verdictCorrect && body.Verdict != verdictIncorrect && body.Verdict != verdictPartial {
		writeError(w, http.StatusBadRequest, "invalid verdict")
		return
	}

	// Wrap the Get→mutate→Patch in RetryOnConflict so that concurrent verdict
	// submissions each re-read the latest resourceVersion and recompute
	// oldVerdict from the freshly fetched status — preventing stale oldVerdict
	// from causing counter drift in the DiagnosticAccuracySummary.
	var diag v1alpha1.AgentDiagnostic
	var oldVerdict string
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &diag); err != nil {
			return err
		}
		oldVerdict = diag.Status.Verdict
		now := metav1.Now()
		base := diag.DeepCopy()
		diag.Status.Verdict = body.Verdict
		diag.Status.ReviewerNote = body.ReviewerNote
		diag.Status.ReviewedBy = sub
		diag.Status.ReviewedAt = &now
		return s.client.Status().Patch(r.Context(), &diag,
			client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
	}); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentDiagnostic not found")
		} else if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid status patch: %v", err))
		} else {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to patch status: %v", err))
		}
		return
	}

	diagnosticVerdictTotal.WithLabelValues(body.Verdict).Inc()
	agentId := diag.Spec.AgentIdentity
	err := s.applyVerdictToSummary(r.Context(), ns, agentId, oldVerdict, body.Verdict)
	if err != nil {
		log.Printf("DiagnosticAccuracySummary update failed for agent %q: %v — verdict saved, recomputing", agentId, err)
		go func() {
			if rerr := s.recomputeAccuracyForAgent(context.Background(), ns, agentId); rerr != nil {
				log.Printf("background recompute for agent %q in %q failed: %v", agentId, ns, rerr)
			}
		}()
		writeJSON(w, http.StatusOK, map[string]any{
			"message": "verdict saved",
			"warning": "accuracy summary update failed; recompute triggered in background",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"message": "verdict saved"})
}

func (s *Server) handleListAgentDiagnostics(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	agentID := r.URL.Query().Get("agentIdentity")
	correlID := r.URL.Query().Get("correlationID")
	limitStr := r.URL.Query().Get("limit")
	continueToken := r.URL.Query().Get("continue")

	caStr := r.URL.Query().Get("createdAfter")
	cbStr := r.URL.Query().Get("createdBefore")
	hasTimeFilter := caStr != "" || cbStr != ""

	// Pagination and time-range filtering are mutually exclusive: fetching a page
	// from etcd and then dropping items in-memory breaks continuation semantics.
	if hasTimeFilter && (limitStr != "" || continueToken != "") {
		writeError(w, http.StatusBadRequest, "pagination (limit/continue) cannot be combined with createdAfter/createdBefore")
		return
	}

	ca, cb, parseErr := parseTimeRange(caStr, cbStr)
	if parseErr != nil {
		writeError(w, http.StatusBadRequest, parseErr.Error())
		return
	}

	listOpts := []client.ListOption{client.InNamespace(ns)}
	matchLabels := map[string]string{}
	if agentID != "" {
		matchLabels["aip.io/agentIdentity"] = sanitizeLabelValue(agentID)
	}
	if correlID != "" {
		matchLabels["aip.io/correlationID"] = sanitizeLabelValue(correlID)
	}
	if len(matchLabels) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(matchLabels))
	}
	if limitStr != "" {
		limit, err := strconv.ParseInt(limitStr, 10, 64)
		if err != nil || limit <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit: must be a positive integer")
			return
		}
		listOpts = append(listOpts, client.Limit(limit))
	}
	if continueToken != "" {
		listOpts = append(listOpts, client.Continue(continueToken))
	}

	var list v1alpha1.AgentDiagnosticList
	if err := s.client.List(r.Context(), &list, listOpts...); err != nil {
		if apierrors.IsBadRequest(err) || apierrors.IsInvalid(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if hasTimeFilter {
		writeJSON(w, http.StatusOK, filterAndSortDiagnostics(list.Items, ca, cb))
		return
	}

	items := list.Items
	if items == nil {
		items = []v1alpha1.AgentDiagnostic{}
	}
	if limitStr != "" || continueToken != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"items":    items,
			"continue": list.Continue,
		})
	} else {
		writeJSON(w, http.StatusOK, items)
	}
}

func parseTimeRange(afterStr, beforeStr string) (after, before time.Time, err error) {
	if afterStr != "" {
		if after, err = time.Parse(time.RFC3339, afterStr); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid createdAfter")
		}
	}
	if beforeStr != "" {
		if before, err = time.Parse(time.RFC3339, beforeStr); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid createdBefore")
		}
	}
	return after, before, nil
}

func filterAndSortDiagnostics(items []v1alpha1.AgentDiagnostic, after, before time.Time) []v1alpha1.AgentDiagnostic {
	results := make([]v1alpha1.AgentDiagnostic, 0, len(items))
	for _, item := range items {
		if !after.IsZero() && !item.CreationTimestamp.After(after) {
			continue
		}
		if !before.IsZero() && !item.CreationTimestamp.Time.Before(before) {
			continue
		}
		results = append(results, item)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].CreationTimestamp.After(results[j].CreationTimestamp.Time)
	})
	return results
}
