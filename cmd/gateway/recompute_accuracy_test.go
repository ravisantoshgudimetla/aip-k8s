package main

import (
	"context"
	"testing"
	"time"

	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

func newRecomputeServer(objs ...client.Object) *Server {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(
			&v1alpha1.AgentRequest{},
			&v1alpha1.AgentDiagnostic{},
			&v1alpha1.DiagnosticAccuracySummary{},
		).
		Build()
	return &Server{
		client:      fc,
		dedupWindow: 0,
		waitTimeout: 90 * time.Second,
		roles:       newRoleConfig("", "", "", "", "", ""),
	}
}

func makeDiagnostic(name, agentIdentity, verdict string) *v1alpha1.AgentDiagnostic {
	d := &v1alpha1.AgentDiagnostic{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.AgentDiagnosticSpec{
			AgentIdentity:  agentIdentity,
			DiagnosticType: "diagnosis",
			CorrelationID:  "corr-" + name,
			Summary:        "test diagnostic " + name,
		},
	}
	d.Status.Verdict = verdict
	return d
}

func makeDiagnosticAt(name, agentIdentity, verdict string, reviewedAt metav1.Time) *v1alpha1.AgentDiagnostic {
	d := makeDiagnostic(name, agentIdentity, verdict)
	d.Status.ReviewedAt = &reviewedAt
	return d
}

func makeStaleSummary(
	name, agentIdentity string,
	correct, incorrect, partial, total int64,
	accuracy *float64,
) *v1alpha1.DiagnosticAccuracySummary {
	s := &v1alpha1.DiagnosticAccuracySummary{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       v1alpha1.DiagnosticAccuracySummarySpec{AgentIdentity: agentIdentity},
	}
	s.Status.CorrectCount = correct
	s.Status.IncorrectCount = incorrect
	s.Status.PartialCount = partial
	s.Status.TotalReviewed = total
	s.Status.DiagnosticAccuracy = accuracy
	return s
}

func getSummary(t *testing.T, srv *Server, name string) *v1alpha1.DiagnosticAccuracySummary {
	t.Helper()
	var summary v1alpha1.DiagnosticAccuracySummary
	if err := srv.client.Get(
		context.Background(),
		client.ObjectKey{Namespace: "default", Name: name},
		&summary,
	); err != nil {
		t.Fatalf("get summary %s: %v", name, err)
	}
	return &summary
}

func TestRecomputeSingleAgentSingleCorrectVerdict(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := newRecomputeServer(
		makeDiagnostic("diag-1", "agent-a", verdictCorrect),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summaryName := summaryNameForAgent("agent-a")
	summary := getSummary(t, srv, summaryName)
	g.Expect(summary.Spec.AgentIdentity).To(gomega.Equal("agent-a"))
	g.Expect(summary.Status.CorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.DiagnosticAccuracy).ToNot(gomega.BeNil())
	g.Expect(*summary.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 1.0, 0.01))
}

func TestRecomputeMultipleAgentsProduceSeparateSummaries(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := newRecomputeServer(
		makeDiagnostic("diag-1", "agent-a", verdictCorrect),
		makeDiagnostic("diag-2", "agent-b", verdictIncorrect),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summaryA := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summaryA.Spec.AgentIdentity).To(gomega.Equal("agent-a"))
	g.Expect(summaryA.Status.CorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summaryA.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(*summaryA.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 1.0, 0.01))

	summaryB := getSummary(t, srv, summaryNameForAgent("agent-b"))
	g.Expect(summaryB.Spec.AgentIdentity).To(gomega.Equal("agent-b"))
	g.Expect(summaryB.Status.IncorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summaryB.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(*summaryB.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 0.0, 0.01))
}

func TestRecomputeVerdictChangeReflectedOnRecompute(t *testing.T) {
	g := gomega.NewWithT(t)
	diag := makeDiagnostic("diag-1", "agent-a", verdictCorrect)
	staleAcc := float64(1.0)
	summaryName := summaryNameForAgent("agent-a")
	staleSummary := makeStaleSummary(summaryName, "agent-a", 1, 0, 0, 1, &staleAcc)
	srv := newRecomputeServer(diag, staleSummary)

	g.Expect(srv.client.Status().Update(context.Background(), staleSummary)).To(gomega.Succeed())

	var fetched v1alpha1.AgentDiagnostic
	g.Expect(srv.client.Get(
		context.Background(),
		client.ObjectKey{Namespace: "default", Name: "diag-1"},
		&fetched,
	)).To(gomega.Succeed())
	fetched.Status.Verdict = verdictIncorrect
	g.Expect(srv.client.Status().Update(context.Background(), &fetched)).To(gomega.Succeed())

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryName)
	g.Expect(summary.Status.CorrectCount).To(gomega.Equal(int64(0)))
	g.Expect(summary.Status.IncorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(*summary.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 0.0, 0.01))
}

func TestRecomputeAgentWithNoRemainingReviewedDiagnosticsIsZeroed(t *testing.T) {
	g := gomega.NewWithT(t)
	summaryName := summaryNameForAgent("agent-a")
	staleAcc := float64(0.8)
	staleSummary := makeStaleSummary(summaryName, "agent-a", 8, 2, 0, 10, &staleAcc)
	srv := newRecomputeServer(staleSummary)

	g.Expect(srv.client.Status().Update(context.Background(), staleSummary)).To(gomega.Succeed())

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryName)
	g.Expect(summary.Status.CorrectCount).To(gomega.Equal(int64(0)))
	g.Expect(summary.Status.IncorrectCount).To(gomega.Equal(int64(0)))
	g.Expect(summary.Status.PartialCount).To(gomega.Equal(int64(0)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(0)))
	g.Expect(summary.Status.DiagnosticAccuracy).To(gomega.BeNil())
}

func TestRecomputePartialVerdictContributesHalfToAccuracy(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := newRecomputeServer(
		makeDiagnostic("diag-1", "agent-a", verdictPartial),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summary.Spec.AgentIdentity).To(gomega.Equal("agent-a"))
	g.Expect(summary.Status.PartialCount).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.DiagnosticAccuracy).ToNot(gomega.BeNil())
	g.Expect(*summary.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 0.5, 0.01))
}

func TestRecomputeAgentIdFilter(t *testing.T) {
	g := gomega.NewWithT(t)
	srv := newRecomputeServer(
		makeDiagnostic("diag-1", "agent-a", verdictCorrect),
		makeDiagnostic("diag-2", "agent-b", verdictIncorrect),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summaryA := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summaryA.Spec.AgentIdentity).To(gomega.Equal("agent-a"))
	g.Expect(summaryA.Status.CorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summaryA.Status.TotalReviewed).To(gomega.Equal(int64(1)))

	var summaryB v1alpha1.DiagnosticAccuracySummary
	err = srv.client.Get(
		context.Background(),
		client.ObjectKey{Namespace: "default", Name: summaryNameForAgent("agent-b")},
		&summaryB,
	)
	g.Expect(err).To(gomega.HaveOccurred())
}

func TestRecomputeUnreviewedDiagnosticsAreSkipped(t *testing.T) {
	g := gomega.NewWithT(t)
	reviewed := makeDiagnostic("diag-reviewed", "agent-a", verdictCorrect)
	unreviewed := &v1alpha1.AgentDiagnostic{
		ObjectMeta: metav1.ObjectMeta{Name: "diag-unreviewed", Namespace: "default"},
		Spec: v1alpha1.AgentDiagnosticSpec{
			AgentIdentity:  "agent-a",
			DiagnosticType: "diagnosis",
			CorrelationID:  "corr-unreviewed",
			Summary:        "no verdict yet",
		},
	}
	srv := newRecomputeServer(reviewed, unreviewed)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summary.Status.CorrectCount).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(1)))
	g.Expect(*summary.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 1.0, 0.01))
}

func TestRecomputeMultiVerdictAccuracyArithmetic(t *testing.T) {
	// 2 correct + 1 partial → (2 + 0.5) / 3 ≈ 0.833
	g := gomega.NewWithT(t)
	srv := newRecomputeServer(
		makeDiagnostic("diag-1", "agent-a", verdictCorrect),
		makeDiagnostic("diag-2", "agent-a", verdictCorrect),
		makeDiagnostic("diag-3", "agent-a", verdictPartial),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summary.Status.CorrectCount).To(gomega.Equal(int64(2)))
	g.Expect(summary.Status.PartialCount).To(gomega.Equal(int64(1)))
	g.Expect(summary.Status.TotalReviewed).To(gomega.Equal(int64(3)))
	g.Expect(summary.Status.DiagnosticAccuracy).ToNot(gomega.BeNil())
	g.Expect(*summary.Status.DiagnosticAccuracy).To(gomega.BeNumerically("~", 2.5/3.0, 0.001))
}

func TestRecomputeLastUpdatedAtTracksNewestVerdict(t *testing.T) {
	g := gomega.NewWithT(t)
	older := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := metav1.NewTime(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	srv := newRecomputeServer(
		makeDiagnosticAt("diag-old", "agent-a", verdictCorrect, older),
		makeDiagnosticAt("diag-new", "agent-a", verdictIncorrect, newer),
	)

	err := srv.recomputeAccuracyForAgent(context.Background(), "default", "agent-a")
	g.Expect(err).ToNot(gomega.HaveOccurred())

	summary := getSummary(t, srv, summaryNameForAgent("agent-a"))
	g.Expect(summary.Status.LastUpdatedAt).ToNot(gomega.BeNil())
	g.Expect(summary.Status.LastUpdatedAt.Time).To(gomega.BeTemporally("~", newer.Time, time.Second))
}
