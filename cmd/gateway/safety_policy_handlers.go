package main

import (
	"encoding/json"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

func (s *Server) handleCreateSafetyPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var sp v1alpha1.SafetyPolicy
	if err := json.NewDecoder(r.Body).Decode(&sp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sp.Namespace = ns

	var grList v1alpha1.GovernedResourceList
	if err := s.client.List(r.Context(), &grList); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := validateSafetypolicyCEL(r.Context(), s.client, &sp, grList.Items); err != nil {
		writeError(w, 422, err.Error())
		return
	}

	if err := s.client.Create(r.Context(), &sp); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, sp)
}

func (s *Server) handleListSafetyPolicies(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	var list v1alpha1.SafetyPolicyList
	if err := s.client.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetSafetyPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	name := r.PathValue("name")

	var sp v1alpha1.SafetyPolicy
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &sp); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (s *Server) handleReplaceSafetyPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	name := r.PathValue("name")

	var newSP v1alpha1.SafetyPolicy
	if err := json.NewDecoder(r.Body).Decode(&newSP); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var grList v1alpha1.GovernedResourceList
	if err := s.client.List(r.Context(), &grList); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := validateSafetypolicyCEL(r.Context(), s.client, &newSP, grList.Items); err != nil {
		writeError(w, 422, err.Error())
		return
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var existing v1alpha1.SafetyPolicy
		if err := s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &existing); err != nil {
			return err
		}
		existing.Spec = newSP.Spec
		existing.Labels = newSP.Labels
		existing.Annotations = newSP.Annotations
		return s.client.Update(r.Context(), &existing)
	})

	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var updated v1alpha1.SafetyPolicy
	_ = s.client.Get(r.Context(), types.NamespacedName{Name: name, Namespace: ns}, &updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteSafetyPolicy(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}
	name := r.PathValue("name")

	sp := &v1alpha1.SafetyPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
	if err := s.client.Delete(r.Context(), sp); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
