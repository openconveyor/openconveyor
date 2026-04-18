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

// AgentInputMount describes how a piece of Task input reaches the agent container.
type AgentInputMount struct {
	// mount is an absolute filesystem path inside the container. Mutually
	// exclusive with env.
	// +optional
	Mount string `json:"mount,omitempty"`

	// env is the environment variable name. Mutually exclusive with mount.
	// Prefer mount for secrets — env vars leak into /proc.
	// +optional
	Env string `json:"env,omitempty"`
}

// AgentInputs names the conventional inputs the agent image expects.
// Each field is optional: an agent that does not consume a given input
// simply leaves it unset on its AgentClass.
type AgentInputs struct {
	// prompt is where Task.spec.prompt is delivered.
	// +optional
	Prompt *AgentInputMount `json:"prompt,omitempty"`

	// config is where Task.spec.agent.config is delivered (JSON).
	// +optional
	Config *AgentInputMount `json:"config,omitempty"`
}

// AgentRequirements lists additional permissions this AgentClass always
// needs, additive with whatever the Task itself declares.
type AgentRequirements struct {
	// egress allowlist the agent always needs (e.g. the LLM endpoint).
	// +optional
	Egress []string `json:"egress,omitempty"`

	// secrets the agent always needs mounted.
	// +optional
	Secrets []string `json:"secrets,omitempty"`
}

// ClusterAgentClassSpec describes an agent plugin: its image and the
// contract by which Task inputs reach the container.
type ClusterAgentClassSpec struct {
	// image is the OCI reference for the agent.
	// +required
	Image string `json:"image"`

	// imagePullPolicy mirrors Pod.spec.containers[].imagePullPolicy.
	// +optional
	// +kubebuilder:default=IfNotPresent
	ImagePullPolicy string `json:"imagePullPolicy,omitempty"`

	// command overrides the image ENTRYPOINT.
	// +optional
	Command []string `json:"command,omitempty"`

	// args overrides the image CMD.
	// +optional
	Args []string `json:"args,omitempty"`

	// inputs describes how Task data reaches the agent container.
	// +optional
	Inputs AgentInputs `json:"inputs,omitempty"`

	// requires declares permissions this agent always needs. Additive with
	// Task.spec.permissions at reconcile time.
	// +optional
	Requires AgentRequirements `json:"requires,omitempty"`
}

// ClusterAgentClassStatus is intentionally minimal — this is a config
// resource, not a reconciled one.
type ClusterAgentClassStatus struct {
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ClusterAgentClass is the Schema for the clusteragentclasses API
type ClusterAgentClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterAgentClass
	// +required
	Spec ClusterAgentClassSpec `json:"spec"`

	// status defines the observed state of ClusterAgentClass
	// +optional
	Status ClusterAgentClassStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterAgentClassList contains a list of ClusterAgentClass
type ClusterAgentClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterAgentClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterAgentClass{}, &ClusterAgentClassList{})
}
