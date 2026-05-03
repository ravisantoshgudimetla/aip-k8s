package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func runAgentGraduationPolicyTests(t *testing.T, _, directClient client.Client, ctx context.Context) {
	t.Run("AgentGraduationPolicy CRUD via gateway", func(t *testing.T) {
		gm := gomega.NewWithT(t)

		// Clean up any existing policy
		if err := directClient.DeleteAllOf(
			ctx, &v1alpha1.AgentGraduationPolicy{}, client.InNamespace(testDefaultNS),
		); err != nil {
			t.Fatalf("failed to clean up existing policies: %v", err)
		}

		// 1. Create
		policy := &v1alpha1.AgentGraduationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: testDefaultNS,
			},
			Spec: v1alpha1.AgentGraduationPolicySpec{
				EvaluationWindow: v1alpha1.EvaluationWindow{Count: 10},
				Levels: []v1alpha1.GraduationLevel{
					{Name: v1alpha1.TrustLevelObserver, CanExecute: false},
					{Name: v1alpha1.TrustLevelAdvisor, CanExecute: true, RequiresHumanApproval: true},
				},
				DemotionPolicy: v1alpha1.DemotionPolicy{
					AccuracyDropThreshold: 0.15,
					WindowSize:            5,
				},
			},
		}

		s := &Server{
			client:       directClient,
			apiReader:    directClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("agent-1", "reviewer-1", "admin-1", "", "", ""),
			authRequired: true,
		}

		body, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace="+testDefaultNS, bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()
		s.handleCreateAgentGraduationPolicy(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusCreated))

		var createResp v1alpha1.AgentGraduationPolicy
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &createResp)).To(gomega.Succeed())
		gm.Expect(createResp.Name).To(gomega.Equal("default"))
		gm.Expect(createResp.Spec.EvaluationWindow.Count).To(gomega.Equal(int64(10)))

		// 2. List
		req = httptest.NewRequest("GET", "/agent-graduation-policies?namespace="+testDefaultNS, nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr = httptest.NewRecorder()
		s.handleListAgentGraduationPolicies(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var listResp []v1alpha1.AgentGraduationPolicy
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &listResp)).To(gomega.Succeed())
		gm.Expect(listResp).To(gomega.HaveLen(1))

		// 3. Get
		req = httptest.NewRequest("GET", "/agent-graduation-policies/default?namespace="+testDefaultNS, nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "default")
		rr = httptest.NewRecorder()
		s.handleGetAgentGraduationPolicy(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var getResp v1alpha1.AgentGraduationPolicy
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &getResp)).To(gomega.Succeed())
		gm.Expect(getResp.Name).To(gomega.Equal("default"))

		// 4. Replace
		policy.Spec.EvaluationWindow.Count = 20
		body, err = json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req = httptest.NewRequest("PUT", "/agent-graduation-policies/default?namespace="+testDefaultNS, bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "default")
		rr = httptest.NewRecorder()
		s.handleReplaceAgentGraduationPolicy(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var replaceResp v1alpha1.AgentGraduationPolicy
		gm.Expect(json.Unmarshal(rr.Body.Bytes(), &replaceResp)).To(gomega.Succeed())
		gm.Expect(replaceResp.Spec.EvaluationWindow.Count).To(gomega.Equal(int64(20)))

		// Verify via K8s client
		var updated v1alpha1.AgentGraduationPolicy
		gm.Expect(directClient.Get(ctx,
			types.NamespacedName{Namespace: testDefaultNS, Name: "default"}, &updated)).To(gomega.Succeed())
		gm.Expect(updated.Spec.EvaluationWindow.Count).To(gomega.Equal(int64(20)))

		// 5. Delete
		req = httptest.NewRequest("DELETE", "/agent-graduation-policies/default?namespace="+testDefaultNS, nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "default")
		rr = httptest.NewRecorder()
		s.handleDeleteAgentGraduationPolicy(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusNoContent))

		// Verify deletion
		gm.Eventually(func() error {
			var check v1alpha1.AgentGraduationPolicy
			return directClient.Get(ctx, types.NamespacedName{Namespace: testDefaultNS, Name: "default"}, &check)
		}, serverWaitTimeout, eventuallyInterval).Should(gomega.HaveOccurred())
	})

	t.Run("AgentGraduationPolicy non-default name rejected", func(t *testing.T) {
		gm := gomega.NewWithT(t)

		s := &Server{
			client:       directClient,
			apiReader:    directClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("agent-1", "reviewer-1", "admin-1", "", "", ""),
			authRequired: true,
		}

		policy := &v1alpha1.AgentGraduationPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "custom-name",
				Namespace: testDefaultNS,
			},
			Spec: v1alpha1.AgentGraduationPolicySpec{
				EvaluationWindow: v1alpha1.EvaluationWindow{Count: 10},
				Levels: []v1alpha1.GraduationLevel{
					{Name: v1alpha1.TrustLevelObserver, CanExecute: false},
				},
				DemotionPolicy: v1alpha1.DemotionPolicy{
					AccuracyDropThreshold: 0.15,
					WindowSize:            5,
				},
			},
		}

		body, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		req := httptest.NewRequest("POST", "/agent-graduation-policies?namespace="+testDefaultNS, bytes.NewReader(body))
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()
		s.handleCreateAgentGraduationPolicy(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
	})

	t.Run("AgentGraduationPolicy non-admin rejected", func(t *testing.T) {
		gm := gomega.NewWithT(t)

		s := &Server{
			client:       directClient,
			apiReader:    directClient,
			dedupWindow:  0,
			waitTimeout:  serverWaitTimeout,
			roles:        newRoleConfig("agent-1", "reviewer-1", "admin-1", "", "", ""),
			authRequired: true,
		}

		req := httptest.NewRequest("GET", "/agent-graduation-policies?namespace="+testDefaultNS, nil)
		req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
		rr := httptest.NewRecorder()
		s.handleListAgentGraduationPolicies(rr, req)
		gm.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})
}
