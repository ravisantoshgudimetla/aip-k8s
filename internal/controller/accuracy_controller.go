/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	governancev1alpha1 "github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

const (
	verdictCorrect   = "correct"
	verdictIncorrect = "incorrect"
	verdictPartial   = "partial"
)

// DiagnosticAccuracyReconciler reconciles AgentRequest verdict changes to maintain DiagnosticAccuracySummary.
type DiagnosticAccuracyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=governance.aip.io,resources=agentrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=governance.aip.io,resources=diagnosticaccuracysummaries,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=governance.aip.io,resources=diagnosticaccuracysummaries/status,verbs=get;update;patch

func (r *DiagnosticAccuracyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agentReq governancev1alpha1.AgentRequest
	if err := r.Get(ctx, req.NamespacedName, &agentReq); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if agentReq.Status.Verdict == "" {
		return ctrl.Result{}, nil
	}

	// Skip if VerdictReasonCode is one of: bad_timing, scope_too_broad, precautionary, policy_block
	// these do not affect accuracy
	skipReasons := []string{"bad_timing", "scope_too_broad", "precautionary", "policy_block"}
	if slices.Contains(skipReasons, agentReq.Status.VerdictReasonCode) {
		return ctrl.Result{}, nil
	}

	summaryName := summaryNameForAgent(agentReq.Spec.AgentIdentity)
	summaryNN := types.NamespacedName{
		Name:      summaryName,
		Namespace: agentReq.Namespace,
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var summary governancev1alpha1.DiagnosticAccuracySummary
		if err := r.Get(ctx, summaryNN, &summary); err != nil {
			if errors.IsNotFound(err) {
				summary = governancev1alpha1.DiagnosticAccuracySummary{
					ObjectMeta: metav1.ObjectMeta{
						Name:      summaryName,
						Namespace: agentReq.Namespace,
					},
					Spec: governancev1alpha1.DiagnosticAccuracySummarySpec{
						AgentIdentity: agentReq.Spec.AgentIdentity,
					},
				}
				if err := r.Create(ctx, &summary); err != nil {
					return err
				}
				// Get fresh copy after create to ensure resourceVersion is set for status patch
				if err := r.Get(ctx, summaryNN, &summary); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// Check if this AgentRequest was already counted
		if slices.Contains(summary.Status.RecentVerdicts, agentReq.Name) {
			return nil
		}

		base := summary.DeepCopy()

		// Increment counters
		summary.Status.TotalReviewed++
		switch agentReq.Status.Verdict {
		case verdictCorrect:
			summary.Status.CorrectCount++
		case verdictPartial:
			summary.Status.PartialCount++
		case verdictIncorrect:
			summary.Status.IncorrectCount++
		}

		// Recompute accuracy: (correct + 0.5*partial) / totalReviewed
		if summary.Status.TotalReviewed > 0 {
			accuracy := (float64(summary.Status.CorrectCount) + 0.5*float64(summary.Status.PartialCount)) / float64(summary.Status.TotalReviewed)
			summary.Status.DiagnosticAccuracy = &accuracy
		}

		// Update LastUpdatedAt
		if agentReq.Status.VerdictAt != nil {
			if summary.Status.LastUpdatedAt == nil || agentReq.Status.VerdictAt.After(summary.Status.LastUpdatedAt.Time) {
				summary.Status.LastUpdatedAt = agentReq.Status.VerdictAt
			}
		}

		// Track this request to avoid double counting. Keep last 100 for safety.
		summary.Status.RecentVerdicts = append(summary.Status.RecentVerdicts, agentReq.Name)
		if len(summary.Status.RecentVerdicts) > 100 {
			summary.Status.RecentVerdicts = summary.Status.RecentVerdicts[len(summary.Status.RecentVerdicts)-100:]
		}

		return r.Status().Patch(ctx, &summary, client.MergeFrom(base))
	})
	if err != nil {
		logger.Error(err, "Failed to update DiagnosticAccuracySummary")
		// Log but do not fail if the DiagnosticAccuracySummary upsert fails
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DiagnosticAccuracyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&governancev1alpha1.AgentRequest{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			// Custom predicate to watch for verdict changes
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				req, ok := object.(*governancev1alpha1.AgentRequest)
				if !ok {
					return false
				}
				return req.Status.Verdict != ""
			}),
		)).
		Named("diagnosticaccuracy").
		Complete(r)
}

var invalidDNSChars = regexp.MustCompile(`[^a-z0-9-]`)

func sanitizeDNSSegment(s string, maxLen int) string {
	s = strings.ToLower(s)
	s = invalidDNSChars.ReplaceAllString(s, "-")
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	s = strings.Trim(s, "-")
	return s
}

func summaryNameForAgent(agentIdentity string) string {
	h := sha256.Sum256([]byte(agentIdentity))
	suffix := fmt.Sprintf("%x", h[:4]) // 8 hex chars
	prefix := sanitizeDNSSegment(agentIdentity, 54)
	if prefix == "" {
		prefix = "agent"
	}
	return prefix + "-" + suffix
}
