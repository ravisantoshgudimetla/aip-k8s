package main

import (
	"context"
	"log"
	"sync"

	"github.com/gobwas/glob"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agent-control-plane/aip-k8s/api/v1alpha1"
)

var (
	globCache = make(map[string]glob.Glob)
	globMu    sync.RWMutex
)

// trustGateResult holds the outcome of a trust level evaluation.
type trustGateResult struct {
	rejected    bool
	message     string
	annotations map[string]string
}

// matchGovernedResource returns the most specific GovernedResource whose URIPattern
// matches targetURI using gobwas/glob semantics (** crosses separators, * does not).
// Most specific = longest pattern; ties broken alphabetically by name.
// Returns nil if no pattern matches.
func matchGovernedResource(items []v1alpha1.GovernedResource, targetURI string) *v1alpha1.GovernedResource {
	var best *v1alpha1.GovernedResource
	for i := range items {
		gr := &items[i]

		globMu.RLock()
		g, ok := globCache[gr.Spec.URIPattern]
		globMu.RUnlock()

		if !ok {
			var err error
			g, err = glob.Compile(gr.Spec.URIPattern, '/')
			if err != nil {
				log.Printf("invalid URIPattern %q in GovernedResource %s: %v", gr.Spec.URIPattern, gr.Name, err)
				continue
			}
			globMu.Lock()
			globCache[gr.Spec.URIPattern] = g
			globMu.Unlock()
		}

		if !g.Match(targetURI) {
			continue
		}

		if best == nil ||
			len(gr.Spec.URIPattern) > len(best.Spec.URIPattern) ||
			(len(gr.Spec.URIPattern) == len(best.Spec.URIPattern) && gr.Name < best.Name) {
			best = gr
		}
	}
	return best
}

// evaluateTrustGate checks the agent's trust level against the GovernedResource's
// trust requirements. Returns a trustGateResult indicating whether the request
// should be rejected and what annotations to apply.
func (s *Server) evaluateTrustGate(
	ctx context.Context, ns, agentIdentity, mode string,
	gr *v1alpha1.GovernedResource,
) (trustGateResult, error) {
	tr := gr.Spec.TrustRequirements
	if tr == nil {
		return trustGateResult{}, nil
	}

	// Observer-mode requests bypass the trust floor check.
	if mode == v1alpha1.ModeObserve {
		return trustGateResult{}, nil
	}

	// Fetch the agent's trust profile.
	profileName := summaryNameForAgent(agentIdentity)
	var profile v1alpha1.AgentTrustProfile
	if err := s.client.Get(ctx, types.NamespacedName{Name: profileName, Namespace: ns}, &profile); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Profile not found — treat as Observer.
			profile.Status.TrustLevel = v1alpha1.TrustLevelObserver
		} else {
			return trustGateResult{}, err
		}
	}

	effectiveLevel := profile.Status.TrustLevel
	if effectiveLevel == "" {
		effectiveLevel = v1alpha1.TrustLevelObserver
	}

	// Check minimum trust level.
	if v1alpha1.TrustLevelOrder[effectiveLevel] < v1alpha1.TrustLevelOrder[tr.MinTrustLevel] {
		return trustGateResult{
			rejected: true,
			message:  "agent trust level " + effectiveLevel + " does not meet resource minimum " + tr.MinTrustLevel,
		}, nil
	}

	// Compute effective autonomy = min(agent level, maxAutonomyLevel).
	effectiveAutonomy := effectiveLevel
	if v1alpha1.TrustLevelOrder[effectiveLevel] > v1alpha1.TrustLevelOrder[tr.MaxAutonomyLevel] {
		effectiveAutonomy = tr.MaxAutonomyLevel
	}

	// Fetch the graduation policy to determine if human approval is required.
	var policy v1alpha1.AgentGraduationPolicy
	requiresApproval := true // fail-closed default
	canExecute := false      // fail-closed default
	if err := s.client.Get(ctx, types.NamespacedName{Name: "default"}, &policy); err == nil {
		for _, level := range policy.Spec.Levels {
			if level.Name == effectiveAutonomy {
				canExecute = level.CanExecute
				requiresApproval = level.RequiresHumanApproval
				break
			}
		}
	}

	annotations := map[string]string{
		v1alpha1.AnnotationEffectiveTrustLevel: effectiveAutonomy,
	}
	if !canExecute {
		annotations[v1alpha1.AnnotationCanExecute] = "false"
	} else {
		annotations[v1alpha1.AnnotationRequiresHumanApproval] = boolToStr(requiresApproval)
	}

	return trustGateResult{annotations: annotations}, nil
}

func boolToStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
