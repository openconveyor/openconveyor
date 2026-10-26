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

	// config is non-secret, agent-specific configuration as a JSON object.
	// The controller projects each top-level scalar field into the agent
	// container as an environment variable with the name of the field, for
	// example TARGET_REPO. When the ClusterAgentClass declares an inputs.config
	// mount path, an environment variable, or both, the controller also
	// delivers the full JSON there. Never put secrets here, because
	// environment variables leak into /proc. Use permissions.secrets for secrets.
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Config *runtime.RawExtension `json:"config,omitempty"`
}

// PromptSource provides the prompt body, either inline or by reference.
// Set exactly one of inline, secretRef and configMapRef.
// +kubebuilder:validation:XValidation:rule="[has(self.inline) && self.inline.size() > 0, has(self.secretRef), has(self.configMapRef)].filter(x, x).size() == 1",message="exactly one of inline, secretRef or configMapRef must be set"
type PromptSource struct {
	// inline is the prompt body as a string. The limit is 256 KiB, so that the
	// owned ConfigMap through which the controller projects it stays under the
	// 1 MiB limit. Use secretRef or configMapRef for a larger prompt.
	// +optional
	// +kubebuilder:validation:MaxLength=262144
	Inline string `json:"inline,omitempty"`

	// secretRef names the Secret key, in the namespace of the Task, from which
	// the controller reads the prompt.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`

	// configMapRef names the ConfigMap key, in the namespace of the Task, from
	// which the controller reads the prompt.
	// +optional
	ConfigMapRef *corev1.ConfigMapKeySelector `json:"configMapRef,omitempty"`
}

// Permissions is the least-privilege declaration for a Task.
// The controller denies all access that this declaration does not list.
type Permissions struct {
	// secrets is the list of Secret names (in the namespace of the Task) that
	// the controller projects as files at /run/secrets/<name>/<key>, one file
	// for each Secret key. The controller does not support injection through
	// environment variables by design, because environment variables leak
	// into /proc.
	// +optional
	Secrets []string `json:"secrets,omitempty"`

	// egress is the allowlist of DNS names, IP addresses or CIDR blocks that
	// the Task can reach. Without entries, the controller denies all egress.
	// The controller resolves the entries to NetworkPolicy egress rules at
	// reconcile time.
	// +optional
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:XValidation:rule="isCIDR(self) || isIP(self) || self.matches('^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?([.][A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*[.]?$')",message="must be a DNS name, an IP address or a CIDR block"
	Egress []string `json:"egress,omitempty"`

	// egressPorts restricts every egress allowlist entry to these
	// destination ports (TCP and UDP). The default is [443]. It does not
	// apply to the DNS rule, which always uses port 53. Ports are a
	// separate field and not a ":port" suffix on the egress entries,
	// because IPv6 CIDRs contain colons.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:Minimum=1
	// +kubebuilder:validation:items:Maximum=65535
	EgressPorts []int32 `json:"egressPorts,omitempty"`

	// rbac is the list of Kubernetes RBAC rules that the Task needs. When it
	// is empty, the Task has no ServiceAccount token and cannot call the
	// Kubernetes API.
	// +optional
	RBAC []RBACRule `json:"rbac,omitempty"`
}

// RBACRule is a minimal Kubernetes RBAC policy rule. It mirrors
// rbacv1.PolicyRule. The type is local, so that the Task CRD schema does not
// embed rbac/v1 transitively.
type RBACRule struct {
	// +required
	// +kubebuilder:validation:MinItems=1
	APIGroups []string `json:"apiGroups"`
	// +required
	// +kubebuilder:validation:MinItems=1
	Resources []string `json:"resources"`
	// +required
	// +kubebuilder:validation:MinItems=1
	Verbs []string `json:"verbs"`
	// +optional
	ResourceNames []string `json:"resourceNames,omitempty"`
}

// TaskResources bounds runtime cost.
type TaskResources struct {
	// cpu is the CPU request and limit, for example "500m" or "1".
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self) && !quantity(self).isLessThan(quantity('0'))",message="must be a non-negative Kubernetes resource quantity, e.g. '500m' or '1'"
	CPU string `json:"cpu,omitempty"`

	// memory is the memory request and limit, for example "512Mi" or "1Gi".
	// +optional
	// +kubebuilder:validation:XValidation:rule="isQuantity(self) && !quantity(self).isLessThan(quantity('0'))",message="must be a non-negative Kubernetes resource quantity, e.g. '512Mi' or '1Gi'"
	Memory string `json:"memory,omitempty"`

	// timeout is mandatory. The controller rejects a Task without one.
	// The controller maps it to Job.spec.activeDeadlineSeconds.
	// +required
	// +kubebuilder:validation:XValidation:rule="duration(self) > duration('0s')",message="must be a valid duration greater than 0 (e.g. '30m')"
	Timeout metav1.Duration `json:"timeout"`
}

// TaskSpec defines the desired state of Task.
type TaskSpec struct {
	// agent selects the ClusterAgentClass that runs this Task.
	// +required
	Agent AgentRef `json:"agent"`

	// prompt provides the prompt body. Set exactly one source.
	// +required
	Prompt PromptSource `json:"prompt"`

	// permissions declares the least-privilege requirements of the Task.
	// The default is zero egress, zero secrets and zero RBAC.
	// +optional
	Permissions Permissions `json:"permissions,omitempty"`

	// resources bounds the CPU, the memory and the wall-clock time.
	// +required
	Resources TaskResources `json:"resources"`

	// ttlSecondsAfterFinished is the number of seconds after which the
	// controller deletes a terminal Task (Completed, Failed or TimedOut).
	// Through ownership, Kubernetes then deletes everything that the Task
	// created. The semantics are the same as for the Job field of the same
	// name. When it is unset, the controller uses its --task-ttl-seconds
	// flag. When both are unset, the controller never deletes the Task
	// automatically.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// TaskPhase is a lifecycle marker that the controller reports in the status.
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

	// observedGeneration is the most recent .metadata.generation that the
	// controller has reconciled. Consumers use it to detect a stale status
	// after a spec change.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// jobName is the name of the Job that this Task owns.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// startTime is the time at which the Job first started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// completionTime is the time at which the Job finished, with success or
	// with failure.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// conditions report the reconciler state in the form that the Kubernetes
	// API conventions define.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agent.ref`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Started",type=date,JSONPath=`.status.startTime`,priority=1
// +kubebuilder:printcolumn:name="Completed",type=date,JSONPath=`.status.completionTime`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Task is one requested run of an agent. The controller creates a
// ServiceAccount, a Role, a NetworkPolicy and a Job from this spec. The
// owner references remove them together with the Task.
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
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Task{}, &TaskList{})
		return nil
	})
}
