package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

const defaultAgentGraduationPolicyName = "default"

func (s *Server) handleCreateAgentGraduationPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	var policy v1alpha1.AgentGraduationPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	if policy.Name != "" && policy.Name != defaultAgentGraduationPolicyName {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("AgentGraduationPolicy name must be %q, got %q", defaultAgentGraduationPolicyName, policy.Name))
		return
	}
	policy.Name = defaultAgentGraduationPolicyName
	policy.Namespace = ns

	if err := s.client.Create(r.Context(), &policy); err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, policy)
}

func (s *Server) handleListAgentGraduationPolicies(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var list v1alpha1.AgentGraduationPolicyList
	if err := s.client.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list.Items)
}

func (s *Server) handleGetAgentGraduationPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var policy v1alpha1.AgentGraduationPolicy
	if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentGraduationPolicy not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleReplaceAgentGraduationPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if name != defaultAgentGraduationPolicyName {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("AgentGraduationPolicy name must be %q, got %q", defaultAgentGraduationPolicyName, name))
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var newPolicy v1alpha1.AgentGraduationPolicy
	if err := json.NewDecoder(r.Body).Decode(&newPolicy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if newPolicy.Name != "" && newPolicy.Name != defaultAgentGraduationPolicyName {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("AgentGraduationPolicy name must be %q, got %q", defaultAgentGraduationPolicyName, newPolicy.Name))
		return
	}
	newPolicy.Name = name
	newPolicy.Namespace = ns

	var updated v1alpha1.AgentGraduationPolicy
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, &updated); err != nil {
			return err
		}
		updated.Spec = newPolicy.Spec
		updated.Labels = newPolicy.Labels
		updated.Annotations = newPolicy.Annotations
		return s.client.Update(r.Context(), &updated)
	})

	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentGraduationPolicy not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteAgentGraduationPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	policy := &v1alpha1.AgentGraduationPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
	}
	if err := s.client.Delete(r.Context(), policy); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentGraduationPolicy not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Check if object still exists (blocked by finalizers)
	var check v1alpha1.AgentGraduationPolicy
	if getErr := s.client.Get(r.Context(), types.NamespacedName{Namespace: ns, Name: name}, &check); getErr != nil {
		if apierrors.IsNotFound(getErr) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeError(w, http.StatusInternalServerError, getErr.Error())
		return
	}
	// Object still exists — finalizers are blocking final removal.
	w.WriteHeader(http.StatusAccepted)
}
