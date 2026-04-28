package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

func (s *Server) handleCreateGovernedResource(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	var gr v1alpha1.GovernedResource
	if err := json.NewDecoder(r.Body).Decode(&gr); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if gr.Spec.ContextSchema != nil {
		if err := validateContextSchema(gr.Spec.ContextSchema.Raw); err != nil {
			writeError(w, 422, fmt.Sprintf("invalid contextSchema: %v", err))
			return
		}
	}

	if err := s.checkContextSchemaConsistency(r.Context(), &gr); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if err := s.client.Create(r.Context(), &gr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, gr)
}

func (s *Server) handleListGovernedResources(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	var list v1alpha1.GovernedResourceList
	if err := s.client.List(r.Context(), &list); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetGovernedResource(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	name := r.PathValue("name")
	var gr v1alpha1.GovernedResource
	if err := s.client.Get(r.Context(), types.NamespacedName{Name: name}, &gr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, gr)
}

func (s *Server) handleReplaceGovernedResource(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB

	name := r.PathValue("name")
	var newGR v1alpha1.GovernedResource
	if err := json.NewDecoder(r.Body).Decode(&newGR); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	newGR.Name = name

	if newGR.Spec.ContextSchema != nil {
		if err := validateContextSchema(newGR.Spec.ContextSchema.Raw); err != nil {
			writeError(w, 422, fmt.Sprintf("invalid contextSchema: %v", err))
			return
		}
	}

	if err := s.checkContextSchemaConsistency(r.Context(), &newGR); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var existing v1alpha1.GovernedResource
		if err := s.client.Get(r.Context(), types.NamespacedName{Name: name}, &existing); err != nil {
			return err
		}

		if err := checkContextSchemaAppendOnly(existing.Spec.ContextSchema, newGR.Spec.ContextSchema); err != nil {
			return fmt.Errorf("INVALID_EVOLUTION: %w", err)
		}

		existing.Spec = newGR.Spec
		existing.Labels = newGR.Labels
		existing.Annotations = newGR.Annotations
		return s.client.Update(r.Context(), &existing)
	})

	if err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if strings.Contains(err.Error(), "INVALID_EVOLUTION") {
			writeError(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "INVALID_EVOLUTION: "))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var updated v1alpha1.GovernedResource
	_ = s.client.Get(r.Context(), types.NamespacedName{Name: name}, &updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteGovernedResource(w http.ResponseWriter, r *http.Request) {
	sub := callerSubFromCtx(r.Context())
	groups := callerGroupsFromCtx(r.Context())
	if !requireRole(s.roles, roleAdmin, sub, groups, w) {
		return
	}

	name := r.PathValue("name")
	gr := &v1alpha1.GovernedResource{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	if err := s.client.Delete(r.Context(), gr); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if apierrors.IsConflict(err) {
			writeError(w, http.StatusConflict, "active requests are blocking deletion (finalizer present)")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Kubernetes sets deletionTimestamp and returns 202 when finalizers are present;
	// the actual removal happens after the controller clears them. Check whether
	// the object is still terminating so callers get the correct status code.
	var check v1alpha1.GovernedResource
	if getErr := s.client.Get(r.Context(), types.NamespacedName{Name: name}, &check); getErr != nil {
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

func (s *Server) checkContextSchemaConsistency(ctx context.Context, newGR *v1alpha1.GovernedResource) error {
	if newGR.Spec.ContextFetcher == "none" || newGR.Spec.ContextSchema == nil {
		return nil
	}

	var list v1alpha1.GovernedResourceList
	if err := s.client.List(ctx, &list); err != nil {
		return err
	}

	for _, gr := range list.Items {
		if gr.Name == newGR.Name {
			continue // Skip self on update
		}
		if gr.Spec.ContextFetcher == newGR.Spec.ContextFetcher && gr.Spec.ContextSchema != nil {
			// New schema must be append-only compatible with each peer's schema so
			// that existing CEL expressions continue to compile after rollout.
			if err := checkContextSchemaAppendOnly(gr.Spec.ContextSchema, newGR.Spec.ContextSchema); err != nil {
				return fmt.Errorf("contextSchema evolution incompatible with GovernedResource %q: %w", gr.Name, err)
			}
			// Don't return early — validate all peers.
		}
	}
	return nil
}

func checkContextSchemaAppendOnly(oldSchema, newSchema *apiextensionsv1.JSON) error {
	if oldSchema == nil {
		return nil
	}
	if newSchema == nil {
		var oldM map[string]any
		_ = json.Unmarshal(oldSchema.Raw, &oldM)
		if props, ok := oldM["properties"].(map[string]any); ok && len(props) > 0 {
			return fmt.Errorf("contextSchema is append-only: field %q was removed", "any")
		}
		return nil
	}

	var oldM, newM map[string]any
	_ = json.Unmarshal(oldSchema.Raw, &oldM)
	_ = json.Unmarshal(newSchema.Raw, &newM)

	oldProps, _ := oldM["properties"].(map[string]any)
	newProps, _ := newM["properties"].(map[string]any)

	for k, v := range oldProps {
		oldField, _ := v.(map[string]any)
		newFieldRaw, exists := newProps[k]
		if !exists {
			return fmt.Errorf("contextSchema is append-only: field %q was removed", k)
		}
		newField, _ := newFieldRaw.(map[string]any)

		if oldField["type"] != newField["type"] {
			return fmt.Errorf("contextSchema is append-only: field %q was removed or changed type", k)
		}
	}
	return nil
}
