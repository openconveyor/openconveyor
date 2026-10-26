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
	"k8s.io/apimachinery/pkg/runtime"
)

// AgentInputMount describes how a piece of Task input reaches the agent container.
type AgentInputMount struct {
	// mount is an absolute filesystem path inside the container. Do not set
	// mount together with env.
	// +optional
	Mount string `json:"mount,omitempty"`

	// env is the environment variable name. Do not set env together with
	// mount. Use mount for secrets, because environment variables leak into
	// /proc.
	// +optional
	Env string `json:"env,omitempty"`
}

// AgentInputs names the conventional inputs that the agent image expects.
// Each field is optional. An agent that does not read an input leaves that
// field unset on its ClusterAgentClass.
type AgentInputs struct {
	// prompt is the location at which the controller delivers Task.spec.prompt.
	// +optional
	Prompt *AgentInputMount `json:"prompt,omitempty"`

	// config is the location at which the controller delivers
	// Task.spec.agent.config as JSON.
	// +optional
	Config *AgentInputMount `json:"config,omitempty"`
}

// AgentRequirements lists the additional permissions that this
// ClusterAgentClass always needs. The controller adds them to the permissions
// that the Task declares.
type AgentRequirements struct {
	// egress is the allowlist that the agent always needs, for example the LLM
	// endpoint. Entries are DNS names, IP addresses or CIDR blocks, as in
	// Task.spec.permissions.egress.
	// +optional
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:XValidation:rule="isCIDR(self) || isIP(self) || self.matches('^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?([.][A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*[.]?$')",message="must be a DNS name, an IP address or a CIDR block"
	Egress []string `json:"egress,omitempty"`

	// secrets is the list of Secret names that the controller always mounts for
	// the agent.
	// +optional
	Secrets []string `json:"secrets,omitempty"`
}

// ClusterAgentClassSpec describes an agent: its image and the contract
// that defines how Task inputs reach the container.
type ClusterAgentClassSpec struct {
	// image is the OCI reference for the agent.
	// +required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// imagePullPolicy mirrors Pod.spec.containers[].imagePullPolicy.
	// +optional
	// +kubebuilder:default=IfNotPresent
	// +kubebuilder:validation:Enum=Always;Never;IfNotPresent
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

	// requires declares the permissions that this agent always needs. The
	// controller adds them to Task.spec.permissions at reconcile time.
	// +optional
	Requires AgentRequirements `json:"requires,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterAgentClass is the Schema for the clusteragentclasses API. The
// controller reads a ClusterAgentClass as configuration and does not
// reconcile it, so the type has no status.
type ClusterAgentClass struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ClusterAgentClass
	// +required
	Spec ClusterAgentClassSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ClusterAgentClassList contains a list of ClusterAgentClass
type ClusterAgentClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterAgentClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ClusterAgentClass{}, &ClusterAgentClassList{})
		return nil
	})
}
