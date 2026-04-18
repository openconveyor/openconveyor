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
)

// WebhookSignature describes how the reference webhook adapter validates
// inbound requests. Supports HMAC schemes common across providers
// (GitHub, Linear, GitLab, Forgejo).
type WebhookSignature struct {
	// header is the HTTP header that carries the signature,
	// e.g. "X-Hub-Signature-256" (GitHub), "Linear-Signature".
	// +required
	Header string `json:"header"`

	// algorithm is the HMAC algorithm. Only "sha256" is supported for now.
	// +optional
	// +kubebuilder:default=sha256
	Algorithm string `json:"algorithm,omitempty"`

	// prefix is stripped from the header value before comparison, e.g. "sha256="
	// for GitHub's "sha256=<hex>" format.
	// +optional
	Prefix string `json:"prefix,omitempty"`

	// secretRef names the Secret containing the shared signing secret.
	// The Secret must live in the same namespace as the webhook adapter.
	// +required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// FieldMapping extracts a value from the webhook payload (JSON) via
// a gjson-style path and maps it to a Task spec field.
type FieldMapping struct {
	// from is a gjson path into the webhook body, e.g.
	// "issue.title" or "pull_request.head.ref".
	// +required
	From string `json:"from"`

	// to is the target Task field. Accepted values:
	//   "prompt"              — body of Task.spec.prompt.inline
	//   "labels.<key>"        — set a label on the Task
	//   "annotations.<key>"   — set an annotation on the Task
	//   "name"                — override the generated Task name
	// +required
	To string `json:"to"`

	// default is used if the path does not resolve.
	// +optional
	Default string `json:"default,omitempty"`
}

// WebhookFilter is a simple gjson equality filter. The webhook adapter
// only emits a Task when every filter matches.
type WebhookFilter struct {
	// path is a gjson path into the webhook body.
	// +required
	Path string `json:"path"`

	// equals is the expected string value.
	// +required
	Equals string `json:"equals"`
}

// TaskTemplate is the prototype Task the webhook adapter materializes on each fire.
type TaskTemplate struct {
	// namespace is where Tasks will be created. Defaults to the adapter's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// generateNamePrefix is prepended to a hash of the event payload to form
	// the Task name, unless a "name" FieldMapping overrides it.
	// +optional
	// +kubebuilder:default=task-
	GenerateNamePrefix string `json:"generateNamePrefix,omitempty"`

	// agent is the AgentRef copied into every emitted Task.
	// +required
	Agent AgentRef `json:"agent"`

	// permissions is copied into every emitted Task.
	// +optional
	Permissions Permissions `json:"permissions,omitempty"`

	// resources is copied into every emitted Task.
	// +required
	Resources TaskResources `json:"resources"`

	// mappings populate fields of the emitted Task from the webhook payload.
	// +optional
	Mappings []FieldMapping `json:"mappings,omitempty"`
}

// ClusterTriggerClassSpec wires an HTTP path on the webhook adapter to a
// Task template. The adapter validates the signature, applies filters,
// then creates a Task using the template plus payload mappings.
type ClusterTriggerClassSpec struct {
	// path is the HTTP path the adapter listens on, e.g. "/linear" or "/github".
	// +required
	Path string `json:"path"`

	// signature declares how to verify the inbound webhook.
	// +required
	Signature WebhookSignature `json:"signature"`

	// filters are ANDed; the webhook is dropped if any fails.
	// Typical use: filter GitHub "issues" events to "labeled" action with
	// a specific label, or Linear updates to a specific stateId.
	// +optional
	Filters []WebhookFilter `json:"filters,omitempty"`

	// task is the Task template the adapter materializes.
	// +required
	Task TaskTemplate `json:"task"`
}

// ClusterTriggerClassStatus is intentionally minimal — this is a config
// resource consumed by the adapter, not a reconciled one.
type ClusterTriggerClassStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ClusterTriggerClass is the Schema for the clustertriggerclasses API
type ClusterTriggerClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterTriggerClass
	// +required
	Spec ClusterTriggerClassSpec `json:"spec"`

	// status defines the observed state of ClusterTriggerClass
	// +optional
	Status ClusterTriggerClassStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterTriggerClassList contains a list of ClusterTriggerClass
type ClusterTriggerClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterTriggerClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterTriggerClass{}, &ClusterTriggerClassList{})
}
