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

// AgentTrustProfileSpec defines the desired state of AgentTrustProfile.
type AgentTrustProfileSpec struct {
	// AgentIdentity is the identity of the agent this profile tracks.
	// +kubebuilder:validation:MinLength=1
	AgentIdentity string `json:"agentIdentity"`
}

// AgentTrustProfileStatus defines the observed state of AgentTrustProfile.
// All fields are set by the controller, never by users.
type AgentTrustProfileStatus struct {
	// TrustLevel is the current trust level: Observer|Advisor|Supervised|Trusted|Autonomous.
	// +kubebuilder:validation:Enum=Observer;Advisor;Supervised;Trusted;Autonomous
	TrustLevel string `json:"trustLevel"`

	// DiagnosticAccuracy is the all-time accuracy ratio for this agent.
	// +optional
	DiagnosticAccuracy *float64 `json:"diagnosticAccuracy,omitempty"`

	// RecentAccuracy is the rolling-window accuracy based on the evaluation window.
	// +optional
	RecentAccuracy *float64 `json:"recentAccuracy,omitempty"`

	// TotalReviewed is the total number of graded verdicts for this agent.
	// +optional
	TotalReviewed int64 `json:"totalReviewed,omitempty"`

	// TotalExecutions is the total number of terminal executions (Completed + Failed).
	// +optional
	TotalExecutions int64 `json:"totalExecutions,omitempty"`

	// SuccessRate is the fraction of executions that reached Completed.
	// +optional
	SuccessRate *float64 `json:"successRate,omitempty"`

	// LastPromotedAt is the timestamp of the most recent promotion.
	// +optional
	LastPromotedAt *metav1.Time `json:"lastPromotedAt,omitempty"`

	// LastDemotedAt is the timestamp of the most recent demotion.
	// +optional
	LastDemotedAt *metav1.Time `json:"lastDemotedAt,omitempty"`

	// LastEvaluatedAt is the timestamp of the most recent trust evaluation.
	// +optional
	LastEvaluatedAt *metav1.Time `json:"lastEvaluatedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="TrustLevel",type=string,JSONPath=`.status.trustLevel`
// +kubebuilder:printcolumn:name="Accuracy",type=number,JSONPath=`.status.diagnosticAccuracy`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentTrustProfile is the Schema for the agenttrustprofiles API.
// Controller-managed — created automatically on first graded verdict.
type AgentTrustProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentTrustProfileSpec   `json:"spec,omitempty"`
	Status AgentTrustProfileStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentTrustProfileList contains a list of AgentTrustProfile.
type AgentTrustProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentTrustProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentTrustProfile{}, &AgentTrustProfileList{})
}
