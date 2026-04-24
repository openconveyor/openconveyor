/*
Copyright 2026.
*/

package policy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func TestBuildPromptProjection_NilWhenNoPromptInput(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{} // no Prompt declared
	vol, mount, err := BuildPromptProjection("task-1", conveyorv1alpha1.PromptSource{Inline: "hi"}, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != nil || mount != nil {
		t.Errorf("expected nil vol/mount when agent has no prompt input, got %+v / %+v", vol, mount)
	}
}

func TestBuildPromptProjection_NilWhenMountEmpty(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{
		Prompt: &conveyorv1alpha1.AgentInputMount{Mount: ""},
	}
	vol, mount, err := BuildPromptProjection("task-1", conveyorv1alpha1.PromptSource{Inline: "hi"}, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != nil || mount != nil {
		t.Errorf("expected nil vol/mount when mount path is empty, got %+v / %+v", vol, mount)
	}
}

func TestBuildPromptProjection_Inline(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{
		Prompt: &conveyorv1alpha1.AgentInputMount{Mount: "/task/prompt"},
	}
	prompt := conveyorv1alpha1.PromptSource{Inline: "do work"}

	vol, mount, err := BuildPromptProjection("my-task", prompt, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol == nil || mount == nil {
		t.Fatal("expected non-nil vol/mount for inline prompt")
	}
	if vol.Name != PromptVolumeName {
		t.Errorf("volume name = %q, want %q", vol.Name, PromptVolumeName)
	}
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source for inline prompt")
	}
	if vol.ConfigMap.Name != InlinePromptConfigMapName("my-task") {
		t.Errorf("configmap name = %q, want %q", vol.ConfigMap.Name, InlinePromptConfigMapName("my-task"))
	}
	if mount.MountPath != "/task/prompt" {
		t.Errorf("mount path = %q, want /task/prompt", mount.MountPath)
	}
	if mount.SubPath != PromptKey {
		t.Errorf("subPath = %q, want %q", mount.SubPath, PromptKey)
	}
	if !mount.ReadOnly {
		t.Error("mount should be read-only")
	}
}

func TestBuildPromptProjection_ConfigMapRef(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{
		Prompt: &conveyorv1alpha1.AgentInputMount{Mount: "/agent/prompt"},
	}
	prompt := conveyorv1alpha1.PromptSource{
		ConfigMapRef: &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "shared-prompts"},
			Key:                  "review.txt",
		},
	}

	vol, mount, err := BuildPromptProjection("task-2", prompt, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source")
	}
	if vol.ConfigMap.Name != "shared-prompts" {
		t.Errorf("configmap name = %q, want shared-prompts", vol.ConfigMap.Name)
	}
	if mount.SubPath != "review.txt" {
		t.Errorf("subPath = %q, want review.txt", mount.SubPath)
	}
}

func TestBuildPromptProjection_SecretRef(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{
		Prompt: &conveyorv1alpha1.AgentInputMount{Mount: "/task/prompt"},
	}
	prompt := conveyorv1alpha1.PromptSource{
		SecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "secret-prompt"},
			Key:                  "body",
		},
	}

	vol, mount, err := BuildPromptProjection("task-3", prompt, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol.Secret == nil {
		t.Fatal("expected Secret volume source")
	}
	if vol.Secret.SecretName != "secret-prompt" {
		t.Errorf("secret name = %q, want secret-prompt", vol.Secret.SecretName)
	}
	if mount.SubPath != "body" {
		t.Errorf("subPath = %q, want body", mount.SubPath)
	}
}

func TestBuildPromptProjection_NoSourceIsError(t *testing.T) {
	inputs := conveyorv1alpha1.AgentInputs{
		Prompt: &conveyorv1alpha1.AgentInputMount{Mount: "/task/prompt"},
	}
	prompt := conveyorv1alpha1.PromptSource{} // nothing set

	_, _, err := BuildPromptProjection("task-4", prompt, inputs)
	if err == nil {
		t.Fatal("expected error when prompt has no source")
	}
}

func TestBuildInlinePromptConfigMap_Inline(t *testing.T) {
	prompt := conveyorv1alpha1.PromptSource{Inline: "hello world"}

	cm := BuildInlinePromptConfigMap("my-task", "ns", prompt)
	if cm == nil {
		t.Fatal("expected non-nil ConfigMap for inline prompt")
	}
	if cm.Name != InlinePromptConfigMapName("my-task") {
		t.Errorf("name = %q, want %q", cm.Name, InlinePromptConfigMapName("my-task"))
	}
	if cm.Namespace != "ns" {
		t.Errorf("namespace = %q, want ns", cm.Namespace)
	}
	if cm.Data[PromptKey] != "hello world" {
		t.Errorf("data[prompt] = %q, want hello world", cm.Data[PromptKey])
	}
	if cm.Labels[LabelManagedBy] != ManagedByValue {
		t.Error("managed-by label missing")
	}
}

func TestBuildInlinePromptConfigMap_NilWhenNotInline(t *testing.T) {
	prompt := conveyorv1alpha1.PromptSource{
		SecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
			Key:                  "k",
		},
	}
	if cm := BuildInlinePromptConfigMap("t", "ns", prompt); cm != nil {
		t.Errorf("expected nil for non-inline prompt, got %+v", cm)
	}
}

func TestBuildInlinePromptConfigMap_NilWhenEmpty(t *testing.T) {
	if cm := BuildInlinePromptConfigMap("t", "ns", conveyorv1alpha1.PromptSource{}); cm != nil {
		t.Errorf("expected nil for empty prompt, got %+v", cm)
	}
}

func TestInlinePromptConfigMapName(t *testing.T) {
	if got := InlinePromptConfigMapName("my-task"); got != "my-task-prompt" {
		t.Errorf("got %q, want my-task-prompt", got)
	}
}
