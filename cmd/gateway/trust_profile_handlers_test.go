package main

import (
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

func newTestServerWithTrustProfiles(profiles ...*v1alpha1.AgentTrustProfile) *Server {
	scheme := newTestScheme()
	objs := make([]client.Object, len(profiles))
	for i, p := range profiles {
		objs[i] = p
	}
	fc := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.AgentTrustProfile{}).
		Build()
	return &Server{
		client:       fc,
		dedupWindow:  0,
		waitTimeout:  5 * time.Second,
		roles:        newRoleConfig("agent-1,agent-2", "reviewer-1", "admin-1", "", "", ""),
		authRequired: true,
	}
}

func TestHandleListAgentTrustProfiles(t *testing.T) {
	profile1 := &v1alpha1.AgentTrustProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-1", Namespace: "default"},
		Spec:       v1alpha1.AgentTrustProfileSpec{AgentIdentity: "agent-1"},
		Status:     v1alpha1.AgentTrustProfileStatus{TrustLevel: v1alpha1.TrustLevelAdvisor},
	}
	profile2 := &v1alpha1.AgentTrustProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-2", Namespace: "default"},
		Spec:       v1alpha1.AgentTrustProfileSpec{AgentIdentity: "agent-2"},
		Status:     v1alpha1.AgentTrustProfileStatus{TrustLevel: v1alpha1.TrustLevelTrusted},
	}

	t.Run("admin lists all profiles", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1, profile2)

		req := httptest.NewRequest("GET", "/agent-trust-profiles?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleListAgentTrustProfiles(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result []v1alpha1.AgentTrustProfile
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result).To(gomega.HaveLen(2))
	})

	t.Run("reviewer lists all profiles", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1, profile2)

		req := httptest.NewRequest("GET", "/agent-trust-profiles?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "reviewer-1"))
		rr := httptest.NewRecorder()

		s.handleListAgentTrustProfiles(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result []v1alpha1.AgentTrustProfile
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result).To(gomega.HaveLen(2))
	})

	t.Run("agent lists only own profile", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1, profile2)

		req := httptest.NewRequest("GET", "/agent-trust-profiles?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "agent-1"))
		rr := httptest.NewRecorder()

		s.handleListAgentTrustProfiles(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result []v1alpha1.AgentTrustProfile
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result).To(gomega.HaveLen(1))
		g.Expect(result[0].Spec.AgentIdentity).To(gomega.Equal("agent-1"))
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1)

		req := httptest.NewRequest("GET", "/agent-trust-profiles", nil)
		rr := httptest.NewRecorder()

		s.handleListAgentTrustProfiles(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusUnauthorized))
	})

	t.Run("unauthorized role rejected", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1)

		req := httptest.NewRequest("GET", "/agent-trust-profiles", nil)
		req = req.WithContext(withCallerSub(req.Context(), "unknown-user"))
		rr := httptest.NewRecorder()

		s.handleListAgentTrustProfiles(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})
}

func TestHandleGetAgentTrustProfile(t *testing.T) {
	profile1 := &v1alpha1.AgentTrustProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "profile-1", Namespace: "default"},
		Spec:       v1alpha1.AgentTrustProfileSpec{AgentIdentity: "agent-1"},
		Status:     v1alpha1.AgentTrustProfileStatus{TrustLevel: v1alpha1.TrustLevelAdvisor},
	}

	t.Run("admin gets any profile", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1)

		req := httptest.NewRequest("GET", "/agent-trust-profiles/profile-1?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "profile-1")
		rr := httptest.NewRecorder()

		s.handleGetAgentTrustProfile(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))

		var result v1alpha1.AgentTrustProfile
		g.Expect(json.Unmarshal(rr.Body.Bytes(), &result)).To(gomega.Succeed())
		g.Expect(result.Name).To(gomega.Equal("profile-1"))
	})

	t.Run("agent gets own profile", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1)

		req := httptest.NewRequest("GET", "/agent-trust-profiles/profile-1?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "agent-1"))
		req.SetPathValue("name", "profile-1")
		rr := httptest.NewRecorder()

		s.handleGetAgentTrustProfile(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusOK))
	})

	t.Run("agent cannot get another agent's profile", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles(profile1)

		req := httptest.NewRequest("GET", "/agent-trust-profiles/profile-1?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "agent-2"))
		req.SetPathValue("name", "profile-1")
		rr := httptest.NewRecorder()

		s.handleGetAgentTrustProfile(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusForbidden))
	})

	t.Run("not found", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles()

		req := httptest.NewRequest("GET", "/agent-trust-profiles/missing?namespace=default", nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		req.SetPathValue("name", "missing")
		rr := httptest.NewRecorder()

		s.handleGetAgentTrustProfile(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusNotFound))
	})

	t.Run("missing name", func(t *testing.T) {
		g := gomega.NewWithT(t)
		s := newTestServerWithTrustProfiles()

		req := httptest.NewRequest("GET", "/agent-trust-profiles/", nil)
		req = req.WithContext(withCallerSub(req.Context(), "admin-1"))
		rr := httptest.NewRecorder()

		s.handleGetAgentTrustProfile(rr, req)
		g.Expect(rr.Code).To(gomega.Equal(http.StatusBadRequest))
	})
}
