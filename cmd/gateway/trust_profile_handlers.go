package main

import (
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

// handleListAgentTrustProfiles lists AgentTrustProfiles in a namespace.
// Admins and reviewers see all profiles. Agents see only their own profile.
func (s *Server) handleListAgentTrustProfiles(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
		return
	}

	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = defaultNamespace
	}

	isAdmin := s.roles.isAdmin(sub, groups)
	isReviewer := s.roles.isReviewer(sub, groups)
	isAgent := s.roles.isAgent(sub, groups)
	if !isAdmin && !isReviewer && !isAgent {
		writeError(w, http.StatusForbidden, "agent, reviewer, or admin role required")
		return
	}

	var list v1alpha1.AgentTrustProfileList
	if err := s.client.List(r.Context(), &list, client.InNamespace(ns)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Agents can only see their own profile.
	if !isAdmin && !isReviewer && isAgent {
		filtered := make([]v1alpha1.AgentTrustProfile, 0, len(list.Items))
		for _, profile := range list.Items {
			if profile.Spec.AgentIdentity == sub {
				filtered = append(filtered, profile)
			}
		}
		list.Items = filtered
	}

	writeJSON(w, http.StatusOK, list.Items)
}

// handleGetAgentTrustProfile returns a single AgentTrustProfile by name.
// Admins and reviewers can read any profile. Agents can read only their own.
func (s *Server) handleGetAgentTrustProfile(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if s.authRequired && sub == "" {
		writeError(w, http.StatusUnauthorized, "caller identity required")
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

	isAdmin := s.roles.isAdmin(sub, groups)
	isReviewer := s.roles.isReviewer(sub, groups)
	isAgent := s.roles.isAgent(sub, groups)
	if !isAdmin && !isReviewer && !isAgent {
		writeError(w, http.StatusForbidden, "agent, reviewer, or admin role required")
		return
	}

	var profile v1alpha1.AgentTrustProfile
	if err := s.client.Get(r.Context(), client.ObjectKey{Namespace: ns, Name: name}, &profile); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "AgentTrustProfile not found")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Agents can only read their own profile.
	if !isAdmin && !isReviewer && isAgent && profile.Spec.AgentIdentity != sub {
		writeError(w, http.StatusForbidden, "cannot read another agent's trust profile")
		return
	}

	writeJSON(w, http.StatusOK, profile)
}
