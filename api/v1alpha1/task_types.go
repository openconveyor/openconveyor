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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AgentRef selects a ClusterAgentClass and passes opaque config to it.
type AgentRef struct {
	// ref is the name of a ClusterAgentClass.
	// +required
	Ref string `json:"ref"`

	// config is passed to the agent image as-is (mounted as JSON at /task/config.json).
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Config *runtime.RawExtension `json:"config,omitempty"`
}

// PromptSource provides the prompt body, either inline or by reference.
// Exactly one of inline / secretRef / configMapRef must be set.
type PromptSource struct {
	// inline is the prompt body as a string.
	// +optional
	Inline string `json:"inline,omitempty"`

	// secretRef reads the prompt from a Secret key in the Task's namespace.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`

	// configMapRef reads the prompt from a ConfigMap key in the Task's namespace.
	// +optional
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`
}

// Permissions is the least-privilege declaration for a Task.
// Anything not listed here is denied by the controller.
type Permissions struct {
	// secrets is the list of Secret names (in the Task's namespace) projected
	// as files at /run/secrets/<name>. Env-var injection is intentionally not
	// supported — env vars leak into /proc.
	// +optional
	Secrets []string `json:"secrets,omitempty"`

	// egress is the allowlist of DNS names or CIDR blocks the Task may reach.
	// Default-deny otherwise. Resolved to NetworkPolicy egress rules at reconcile time.
	// +optional
	Egress []string `json:"egress,omitempty"`

	// rbac is the list of Kubernetes RBAC rules the Task needs. Empty means
	// the Task has no ServiceAccount token and cannot talk to the k8s API.
	// +optional
	RBAC []RBACRule `json:"rbac,omitempty"`
}

// RBACRule is a minimal Kubernetes RBAC policy rule. Mirrors rbacv1.PolicyRule
// but kept local so the Task CRD schema does not transitively embed rbac/v1.
type RBACRule struct {
	// +required
	APIGroups []string `json:"apiGroups"`
	// +required
	Resources []string `json:"resources"`
	// +required
	Verbs []string `json:"verbs"`
	// +optional
	ResourceNames []string `json:"resourceNames,omitempty"`
}

// TaskResources bounds runtime cost.
type TaskResources struct {
	// cpu request/limit, e.g. "500m" or "1".
	// +optional
	CPU string `json:"cpu,omitempty"`

	// memory request/limit, e.g. "512Mi" or "1Gi".
	// +optional
	Memory string `json:"memory,omitempty"`

	// timeout is mandatory. The controller rejects Tasks without one.
	// Mapped to Job.spec.activeDeadlineSeconds.
	// +required
	Timeout metav1.Duration `json:"timeout"`
}

// TaskSpec defines the desired state of Task.
type TaskSpec struct {
	// agent selects the AgentClass that runs this Task.
	// +required
	Agent AgentRef `json:"agent"`

	// prompt provides the prompt body. Exactly one source must be set.
	// +required
	Prompt PromptSource `json:"prompt"`

	// permissions declares the Task's least-privilege requirements.
	// Default is zero egress, zero secrets, zero RBAC.
	// +optional
	Permissions Permissions `json:"permissions,omitempty"`

	// resources bounds CPU/memory/wall-clock.
	// +required
	Resources TaskResources `json:"resources"`
}

// TaskPhase is a simple lifecycle marker surfaced on status.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed;TimedOut
type TaskPhase string

const (
	TaskPhasePending   TaskPhase = "Pending"
	TaskPhaseRunning   TaskPhase = "Running"
	TaskPhaseCompleted TaskPhase = "Completed"
	TaskPhaseFailed    TaskPhase = "Failed"
	TaskPhaseTimedOut  TaskPhase = "TimedOut"
)

// TaskStatus defines the observed state of Task.
type TaskStatus struct {
	// phase is a coarse lifecycle marker.
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// jobName references the Job owned by this Task.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// startTime is when the Job first started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// completionTime is when the Job finished (success or failure).
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// conditions surface reconciler state per Kubernetes API conventions.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agent.ref`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Task is a single agent run: the orchestrator materializes a ServiceAccount,
// Role, NetworkPolicy, and Job from this spec and tears them down when done.
type Task struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Task
	// +required
	Spec TaskSpec `json:"spec"`

	// status defines the observed state of Task
	// +optional
	Status TaskStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TaskList contains a list of Task
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Task `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Task{}, &TaskList{})
}
