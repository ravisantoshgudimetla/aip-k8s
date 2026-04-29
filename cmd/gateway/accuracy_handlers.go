package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

// applyVerdictToSummary atomically applies a verdict change to the
// DiagnosticAccuracySummary for the given agent. It creates the summary if it
// does not yet exist and retries on conflict so concurrent calls converge.
func (s *Server) applyVerdictToSummary(ctx context.Context, ns, agentId, oldVerdict, newVerdict string) error {
	summaryName := summaryNameForAgent(agentId)
	updatedAt := metav1.Now()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var summary v1alpha1.DiagnosticAccuracySummary
		err := s.client.Get(ctx, types.NamespacedName{Name: summaryName, Namespace: ns}, &summary)
		exists := true
		if err != nil {
			if apierrors.IsNotFound(err) {
				exists = false
				summary = v1alpha1.DiagnosticAccuracySummary{
					ObjectMeta: metav1.ObjectMeta{Name: summaryName, Namespace: ns},
					Spec:       v1alpha1.DiagnosticAccuracySummarySpec{AgentIdentity: agentId},
				}
			} else {
				return err
			}
		}

		// Guard against accidental cross-agent reuse.
		if exists && summary.Spec.AgentIdentity != agentId {
			exists = false
			summary = v1alpha1.DiagnosticAccuracySummary{
				ObjectMeta: metav1.ObjectMeta{Name: summaryName, Namespace: ns},
				Spec:       v1alpha1.DiagnosticAccuracySummarySpec{AgentIdentity: agentId},
			}
		}

		if !exists {
			if err := s.client.Create(ctx, &summary); err != nil {
				if apierrors.IsAlreadyExists(err) {
					// Concurrent Create won the race; synthesise a Conflict so
					// retry.RetryOnConflict re-runs the Get → mutate → Patch sequence.
					return apierrors.NewConflict(schema.GroupResource{}, summaryName, err)
				}
				return err
			}
			// Counters start at zero on a new summary; do not decrement the old
			// verdict or we produce negative counts after a manual summary deletion.
			oldVerdict = ""
		}

		if oldVerdict != "" {
			switch oldVerdict {
			case verdictCorrect:
				summary.Status.CorrectCount--
			case verdictIncorrect:
				summary.Status.IncorrectCount--
			case verdictPartial:
				summary.Status.PartialCount--
			}
			summary.Status.TotalReviewed--
		}

		switch newVerdict {
		case verdictCorrect:
			summary.Status.CorrectCount++
		case verdictIncorrect:
			summary.Status.IncorrectCount++
		case verdictPartial:
			summary.Status.PartialCount++
		}
		summary.Status.TotalReviewed++

		acc := float64(summary.Status.CorrectCount) + 0.5*float64(summary.Status.PartialCount)
		if summary.Status.TotalReviewed > 0 {
			val := acc / float64(summary.Status.TotalReviewed)
			summary.Status.DiagnosticAccuracy = &val
		} else {
			summary.Status.DiagnosticAccuracy = nil
		}
		summary.Status.LastUpdatedAt = &updatedAt
		return s.client.Status().Update(ctx, &summary)
	})
}

// recomputeAccuracyForAgent rebuilds DiagnosticAccuracySummary for the given
// agent (pass agentId="" to rebuild all agents) by scanning every reviewed
// AgentDiagnostic in ns. It is safe to call from a goroutine with
// context.Background() when the originating HTTP request has already returned.
//
//nolint:gocyclo // function scans and rebuilds accuracy summaries; complexity is inherent
func (s *Server) recomputeAccuracyForAgent(ctx context.Context, ns, agentId string) error {
	var list v1alpha1.AgentDiagnosticList
	if err := s.client.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return fmt.Errorf("list diagnostics: %w", err)
	}

	stats := make(map[string]*v1alpha1.DiagnosticAccuracySummary)
	for _, item := range list.Items {
		if agentId != "" && item.Spec.AgentIdentity != agentId {
			continue
		}
		id := item.Spec.AgentIdentity
		if item.Status.Verdict == "" {
			continue
		}

		summaryName := summaryNameForAgent(id)
		summary, ok := stats[summaryName]
		if !ok {
			summary = &v1alpha1.DiagnosticAccuracySummary{
				ObjectMeta: metav1.ObjectMeta{Name: summaryName, Namespace: ns},
				Spec:       v1alpha1.DiagnosticAccuracySummarySpec{AgentIdentity: id},
			}
			stats[summaryName] = summary
		}

		switch item.Status.Verdict {
		case verdictCorrect:
			summary.Status.CorrectCount++
		case verdictIncorrect:
			summary.Status.IncorrectCount++
		case verdictPartial:
			summary.Status.PartialCount++
		}
		summary.Status.TotalReviewed++

		reviewedAt := item.Status.ReviewedAt
		if summary.Status.LastUpdatedAt == nil || (reviewedAt != nil && reviewedAt.After(summary.Status.LastUpdatedAt.Time)) {
			summary.Status.LastUpdatedAt = reviewedAt
		}
	}

	for id, summary := range stats {
		acc := float64(summary.Status.CorrectCount) + 0.5*float64(summary.Status.PartialCount)
		if summary.Status.TotalReviewed > 0 {
			val := acc / float64(summary.Status.TotalReviewed)
			summary.Status.DiagnosticAccuracy = &val
		}

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var existing v1alpha1.DiagnosticAccuracySummary
			err := s.client.Get(ctx, types.NamespacedName{Name: id, Namespace: ns}, &existing)
			if err != nil {
				if apierrors.IsNotFound(err) {
					if err := s.client.Create(ctx, summary); err != nil {
						return err
					}
					return s.client.Status().Update(ctx, summary)
				}
				return err
			}
			// Verify the existing CR belongs to the same agent before overwriting.
			if existing.Spec.AgentIdentity != summary.Spec.AgentIdentity {
				return fmt.Errorf("summary %q identity mismatch: got %q, want %q",
					id, existing.Spec.AgentIdentity, summary.Spec.AgentIdentity)
			}
			existing.Status = summary.Status
			return s.client.Status().Update(ctx, &existing)
		})
		if err != nil {
			log.Printf("failed to upsert summary for %s: %v", id, err)
		}
	}

	// Zero out summaries for agents that no longer have any reviewed diagnostics
	// (e.g., after their diagnostics were deleted). Without this, a recompute
	// would leave stale counts behind, defeating the recovery guarantee.
	var existingSummaries v1alpha1.DiagnosticAccuracySummaryList
	if err := s.client.List(ctx, &existingSummaries, client.InNamespace(ns)); err != nil {
		log.Printf("failed to list existing summaries during recompute: %v", err)
	} else {
		for i := range existingSummaries.Items {
			existing := &existingSummaries.Items[i]
			if agentId != "" && existing.Spec.AgentIdentity != agentId {
				continue
			}
			if _, ok := stats[existing.Name]; ok {
				continue
			}
			err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var fresh v1alpha1.DiagnosticAccuracySummary
				if err := s.client.Get(ctx, types.NamespacedName{Name: existing.Name, Namespace: ns}, &fresh); err != nil {
					return err
				}
				fresh.Status = v1alpha1.DiagnosticAccuracySummaryStatus{}
				return s.client.Status().Update(ctx, &fresh)
			})
			if err != nil {
				log.Printf("failed to zero stale summary %s: %v", existing.Name, err)
			}
		}
	}

	return nil
}

func (s *Server) handleRecomputeAccuracy(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	sub := callerSubFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}
	if !requireRole(s.roles, roleReviewer, sub, callerGroupsFromCtx(r.Context()), w) {
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	agentId := r.URL.Query().Get("agentIdentity")

	if err := s.recomputeAccuracyForAgent(r.Context(), ns, agentId); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"message": "recomputed accuracy summaries"})
}

func (s *Server) handleListAccuracySummaries(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var list v1alpha1.DiagnosticAccuracySummaryList
	if err := s.client.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, list.Items)
}
