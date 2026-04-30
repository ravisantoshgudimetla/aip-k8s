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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentGraduationPolicySpec defines the thresholds for each trust level.
type AgentGraduationPolicySpec struct {
	// EvaluationWindow defines the rolling window for trust evaluation.
	EvaluationWindow EvaluationWindow `json:"evaluationWindow"`

	// AwaitingVerdictTTL is the duration after which ungraded requests expire.
	// e.g. "168h" for 7 days.
	// +optional
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	AwaitingVerdictTTL string `json:"awaitingVerdictTTL,omitempty"`

	// Levels defines the graduation levels and their requirements.
	Levels []GraduationLevel `json:"levels"`

	// DemotionPolicy defines when an agent should be demoted.
	DemotionPolicy DemotionPolicy `json:"demotionPolicy"`
}

// EvaluationWindow defines how many recent verdicts drive trust evaluation.
type EvaluationWindow struct {
	// Count is the number of recent verdicts to consider.
	// +kubebuilder:validation:Minimum=1
	Count int64 `json:"count"`
}

// GraduationLevel defines the requirements and permissions for a trust level.
type GraduationLevel struct {
	// Name is the level name: Observer|Advisor|Supervised|Trusted|Autonomous.
	// +kubebuilder:validation:Enum=Observer;Advisor;Supervised;Trusted;Autonomous
	Name string `json:"name"`

	// CanExecute indicates whether agents at this level may execute actions.
	CanExecute bool `json:"canExecute"`

	// RequiresHumanApproval indicates whether human approval is required.
	// +optional
	RequiresHumanApproval bool `json:"requiresHumanApproval,omitempty"`

	// Accuracy defines the accuracy band for this level.
	// +optional
	Accuracy *AccuracyBand `json:"accuracy,omitempty"`

	// Executions defines the execution count band for this level.
	// +optional
	Executions *ExecutionBand `json:"executions,omitempty"`
}

// AccuracyBand defines the accuracy thresholds for a graduation level.
type AccuracyBand struct {
	// Min is the minimum accuracy required for this level.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	Min *float64 `json:"min,omitempty"`

	// Max is the maximum accuracy for this level (upper bound).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	Max *float64 `json:"max,omitempty"`

	// DemotionBuffer is the margin below min that triggers demotion.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	// +optional
	DemotionBuffer *float64 `json:"demotionBuffer,omitempty"`
}

// ExecutionBand defines the execution count thresholds for a graduation level.
type ExecutionBand struct {
	// Min is the minimum executions required for this level.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Min *int64 `json:"min,omitempty"`

	// Max is the maximum executions for this level (upper bound).
	// +kubebuilder:validation:Minimum=0
	// +optional
	Max *int64 `json:"max,omitempty"`
}

// DemotionPolicy defines when an agent should be demoted.
type DemotionPolicy struct {
	// AccuracyDropThreshold is the accuracy drop that triggers demotion.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	AccuracyDropThreshold float64 `json:"accuracyDropThreshold"`

	// WindowSize is the number of recent verdicts to check for demotion.
	// +kubebuilder:validation:Minimum=1
	WindowSize int64 `json:"windowSize"`

	// GracePeriod is the duration an agent is permitted to stay at its current
	// level after falling below thresholds before demotion is applied.
	// e.g. "24h".
	// +optional
	// +kubebuilder:validation:Pattern="^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$"
	GracePeriod string `json:"gracePeriod,omitempty"`
}

// AgentGraduationPolicyStatus defines the observed state of AgentGraduationPolicy.
type AgentGraduationPolicyStatus struct {
	// Conditions hold the latest available observations of the AgentGraduationPolicy's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Window",type=integer,JSONPath=`.spec.evaluationWindow.count`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentGraduationPolicy is the Schema for the agentgraduationpolicies API.
// Cluster-admin-managed — defines thresholds for trust level graduation.
type AgentGraduationPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentGraduationPolicySpec   `json:"spec,omitempty"`
	Status AgentGraduationPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentGraduationPolicyList contains a list of AgentGraduationPolicy.
type AgentGraduationPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentGraduationPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentGraduationPolicy{}, &AgentGraduationPolicyList{})
}
