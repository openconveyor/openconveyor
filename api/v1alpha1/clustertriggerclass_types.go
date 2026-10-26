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

// WebhookSignature describes how the reference webhook adapter validates
// inbound requests. It supports the HMAC schemes that GitHub, Linear, GitLab
// and Forgejo use.
type WebhookSignature struct {
	// header is the HTTP header that carries the signature, for example
	// "X-Hub-Signature-256" (GitHub) or "Linear-Signature".
	// +required
	Header string `json:"header"`

	// algorithm is the HMAC algorithm. The adapter supports only "sha256" at
	// this time.
	// +optional
	// +kubebuilder:default=sha256
	Algorithm string `json:"algorithm,omitempty"`

	// prefix is the text that the adapter removes from the header value before
	// the comparison, for example "sha256=" for the GitHub "sha256=<hex>" format.
	// +optional
	Prefix string `json:"prefix,omitempty"`

	// secretRef names the Secret that contains the shared signing secret.
	// The Secret must be in the same namespace as the webhook adapter.
	// +required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// FieldMapping extracts a value from the webhook payload (JSON) with
// a gjson-style path and maps it to a Task spec field.
type FieldMapping struct {
	// from is a gjson path into the webhook body, for example
	// "issue.title" or "pull_request.head.ref".
	// +required
	From string `json:"from"`

	// to is the target Task field. The adapter accepts these values:
	//   - "prompt": the body of Task.spec.prompt.inline
	//   - "labels.<key>": a label on the Task
	//   - "annotations.<key>": an annotation on the Task
	//   - "name": the Task name, in place of the generated name
	//   - "config.<key>": Task.spec.agent.config.<key>, which the controller
	//     projects into the agent container as the environment variable $<key>
	// +required
	To string `json:"to"`

	// default is the value that the adapter uses when the path does not resolve.
	// +optional
	Default string `json:"default,omitempty"`
}

// WebhookFilter is a gjson equality filter. The webhook adapter
// creates a Task only when every filter matches.
type WebhookFilter struct {
	// path is a gjson path into the webhook body.
	// +required
	Path string `json:"path"`

	// equals is the expected string value.
	// +required
	Equals string `json:"equals"`
}

// TaskTemplate is the prototype Task that the webhook adapter creates for each
// accepted event.
type TaskTemplate struct {
	// namespace is the namespace in which the adapter creates the Tasks. The
	// default is the namespace of the adapter.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// generateNamePrefix is the text that the adapter puts before a hash of the
	// event payload to form the Task name. A "name" FieldMapping replaces this
	// name.
	// +optional
	// +kubebuilder:default=task-
	GenerateNamePrefix string `json:"generateNamePrefix,omitempty"`

	// agent is the AgentRef that the adapter copies into every Task it creates.
	// +required
	Agent AgentRef `json:"agent"`

	// permissions is the Permissions value that the adapter copies into every
	// Task it creates.
	// +optional
	Permissions Permissions `json:"permissions,omitempty"`

	// resources is the TaskResources value that the adapter copies into every
	// Task it creates.
	// +required
	Resources TaskResources `json:"resources"`

	// ttlSecondsAfterFinished is the TTL that the adapter copies into every
	// Task it creates. Without it, webhook Tasks accumulate. See
	// Task.spec.ttlSecondsAfterFinished.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// mappings populate fields of the created Task from the webhook payload.
	// +optional
	Mappings []FieldMapping `json:"mappings,omitempty"`
}

// ClusterTriggerClassSpec connects an HTTP path on the webhook adapter to a
// Task template. The adapter validates the signature, applies the filters,
// and then creates a Task from the template and the payload mappings.
type ClusterTriggerClassSpec struct {
	// path is the HTTP path on which the adapter listens, for example "/linear"
	// or "/github".
	// +required
	Path string `json:"path"`

	// signature declares how the adapter verifies the inbound webhook.
	// +required
	Signature WebhookSignature `json:"signature"`

	// filters are the conditions that an event must all satisfy. The adapter
	// drops the webhook when one filter fails. A typical use filters GitHub
	// "issues" events to the "labeled" action with a specific label, or Linear
	// updates to a specific stateId.
	// +optional
	Filters []WebhookFilter `json:"filters,omitempty"`

	// task is the Task template from which the adapter creates each Task.
	// +required
	Task TaskTemplate `json:"task"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Path",type=string,JSONPath=`.spec.path`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterTriggerClass is the Schema for the clustertriggerclasses API. The
// adapter reads a ClusterTriggerClass as configuration, and no controller
// reconciles it, so the type has no status.
type ClusterTriggerClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterTriggerClass
	// +required
	Spec ClusterTriggerClassSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ClusterTriggerClassList contains a list of ClusterTriggerClass
type ClusterTriggerClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterTriggerClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ClusterTriggerClass{}, &ClusterTriggerClassList{})
		return nil
	})
}
