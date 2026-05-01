package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestServerWithGraduationPolicies(policies ...*v1alpha1.AgentGraduationPolicy) *Server {
	scheme := newTestScheme()
	objs := make([]client.Object, len(policies))
	for i, p := range policies {
		objs[i] = p
	}
	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.AgentGraduationPolicy{}).
		Build()
	return &Server{
		client:       fc,
		dedupWindow:  0,
		waitTimeout:  5 * time.Second,
		roles:        newRoleConfig("agent-1", "reviewer-1", "admin-1", "", "", ""),
		authRequired: true,
	}
}

func makeAgentGraduationPolicy(name string) *v1alpha1.AgentGraduationPolicy {
	minAcc := 0.8
	return &v1alpha1.AgentGraduationPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: v1alpha1.AgentGraduationPolicySpec{
			EvaluationWindow: v1alpha1.EvaluationWindow{Count: 10},
			Levels: []v1alpha1.GraduationLevel{
				{
					Name:       v1alpha1.TrustLevelObserver,
					CanExecute: false,
				},
				{
					Name:                  v1alpha1.TrustLevelTrusted,
					CanExecute:            true,
					RequiresHumanApproval: false,
					Accuracy: &v1alpha1.AccuracyBand{
						Min: &minAcc,
					},
				},
			},
			DemotionPolicy: v1alpha1.DemotionPolicy{
				AccuracyDropThreshold: 0.15,
				WindowSize:            5,
			},
		},
	}
}

func TestHandleCreateAgentGraduationPolicy(t *testing.T) {
	t.Run("admin creates policy", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		policy := makeAgentGraduationPolicy("default")
		body, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleCreateAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusCreated))
	})

	t.Run("non-admin rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		policy := makeAgentGraduationPolicy("default")
		body, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
		rr := httptest.NewRecorder()

		s.handleCreateAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})

	t.Run("non-default name rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		policy := makeAgentGraduationPolicy("custom-name")
		body, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleCreateAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
	})

	t.Run("invalid body rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace=default",
			bytes.NewReader([]byte("not-json")))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleCreateAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
	})
}

func TestHandleListAgentGraduationPolicies(t *testing.T) {
	t.Run("admin lists policies", func(t *testing.T) {
		g := gomega.NewWithT(t)
		policy := makeAgentGraduationPolicy("default")
		s := newTestServerWithGraduationPolicies(policy)

		req := httptest.NewRequest("GET", "/agent-graduation-policies?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleListAgentGraduationPolicies(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result []v1alpha1.AgentGraduationPolicy
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result).To(gomega.HaveLen(1))
	})

	t.Run("non-admin rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		req := httptest.NewRequest("GET", "/agent-graduation-policies?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
		rr := httptest.NewRecorder()

		s.handleListAgentGraduationPolicies(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})
}

func TestHandleGetAgentGraduationPolicy(t *testing.T) {
	g := gomega.NewWithT(t)
	policy := makeAgentGraduationPolicy("default")
	s := newTestServerWithGraduationPolicies(policy)

	req := httptest.NewRequest("GET", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleGetAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
}

func TestHandleGetAgentGraduationPolicy_NotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	s := newTestServerWithGraduationPolicies()

	req := httptest.NewRequest("GET", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleGetAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
}

func TestHandleGetAgentGraduationPolicy_NonAdminRejected(t *testing.T) {
	g := gomega.NewWithT(t)
	policy := makeAgentGraduationPolicy("default")
	s := newTestServerWithGraduationPolicies(policy)

	req := httptest.NewRequest("GET", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleGetAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
}

func TestHandleReplaceAgentGraduationPolicy(t *testing.T) {
	t.Run("admin replaces policy", func(t *testing.T) {
		g := gomega.NewWithT(t)
		policy := makeAgentGraduationPolicy("default")
		s := newTestServerWithGraduationPolicies(policy)

		updated := makeAgentGraduationPolicy("default")
		updated.Spec.EvaluationWindow.Count = 20
		body, err := json.Marshal(updated)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		req := httptest.NewRequest("PUT", "/agent-graduation-policies/default?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "default")
		rr := httptest.NewRecorder()

		s.handleReplaceAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result v1alpha1.AgentGraduationPolicy
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result.Spec.EvaluationWindow.Count).To(gomega.Equal(int64(20)))
	})

	t.Run("not found", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithGraduationPolicies()

		updated := makeAgentGraduationPolicy("default")
		body, err := json.Marshal(updated)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		req := httptest.NewRequest("PUT", "/agent-graduation-policies/default?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "default")
		rr := httptest.NewRecorder()

		s.handleReplaceAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
	})

	t.Run("non-admin rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		policy := makeAgentGraduationPolicy("default")
		s := newTestServerWithGraduationPolicies(policy)

		updated := makeAgentGraduationPolicy("default")
		body, err := json.Marshal(updated)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}

		req := httptest.NewRequest("PUT", "/agent-graduation-policies/default?namespace=default", bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
		req.SetPathValue("name", "default")
		rr := httptest.NewRecorder()

		s.handleReplaceAgentGraduationPolicy(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})
}

func TestHandleDeleteAgentGraduationPolicy(t *testing.T) {
	g := gomega.NewWithT(t)
	policy := makeAgentGraduationPolicy("default")
	s := newTestServerWithGraduationPolicies(policy)

	req := httptest.NewRequest("DELETE", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleDeleteAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusNoContent))
}

func TestHandleDeleteAgentGraduationPolicy_NotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	s := newTestServerWithGraduationPolicies()

	req := httptest.NewRequest("DELETE", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleDeleteAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
}

func TestHandleDeleteAgentGraduationPolicy_NonAdminRejected(t *testing.T) {
	g := gomega.NewWithT(t)
	policy := makeAgentGraduationPolicy("default")
	s := newTestServerWithGraduationPolicies(policy)

	req := httptest.NewRequest("DELETE", "/agent-graduation-policies/default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
	req.SetPathValue("name", "default")
	rr := httptest.NewRecorder()

	s.handleDeleteAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
}

func TestHandleGetAgentGraduationPolicy_NonDefaultNameNotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	s := newTestServerWithGraduationPolicies()

	req := httptest.NewRequest("GET", "/agent-graduation-policies/not-default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "not-default")
	rr := httptest.NewRecorder()

	s.handleGetAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
}

func TestHandleReplaceAgentGraduationPolicy_NonDefaultNameRejected(t *testing.T) {
	g := gomega.NewWithT(t)
	s := newTestServerWithGraduationPolicies()

	policy := makeAgentGraduationPolicy("default")
	body, err := json.Marshal(policy)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	req := httptest.NewRequest("PUT", "/agent-graduation-policies/not-default?namespace=default", bytes.NewReader(body))
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "not-default")
	rr := httptest.NewRecorder()

	s.handleReplaceAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
}

func TestHandleDeleteAgentGraduationPolicy_NonDefaultNameNotFound(t *testing.T) {
	g := gomega.NewWithT(t)
	s := newTestServerWithGraduationPolicies()

	req := httptest.NewRequest("DELETE", "/agent-graduation-policies/not-default?namespace=default", nil)
	req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
	req.SetPathValue("name", "not-default")
	rr := httptest.NewRecorder()

	s.handleDeleteAgentGraduationPolicy(rr, req)
	g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
}
