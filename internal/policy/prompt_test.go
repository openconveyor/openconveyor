/*
Copyright 2026.
*/

package policy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func TestBuildPromptProjection(t *testing.T) {
	promptInput := func(mount string) conveyorv1alpha1.AgentInputs {
		return conveyorv1alpha1.AgentInputs{
			Prompt: &conveyorv1alpha1.AgentInputMount{Mount: mount},
		}
	}

	tests := []struct {
		name     string
		taskName string
		prompt   conveyorv1alpha1.PromptSource
		inputs   conveyorv1alpha1.AgentInputs
		wantErr  bool
		wantZero bool
		// Expected fields, checked only when wantZero and wantErr are false.
		wantConfigMap string // non-empty → ConfigMap volume source with this name
		wantSecret    string // non-empty → Secret volume source with this name
		wantMountPath string
		wantSubPath   string
	}{
		{
			name:     "zero projection when agent has no prompt input",
			taskName: "task-1",
			prompt:   conveyorv1alpha1.PromptSource{Inline: "hi"},
			inputs:   conveyorv1alpha1.AgentInputs{}, // no Prompt declared
			wantZero: true,
		},
		{
			name:     "zero projection when mount path is empty",
			taskName: "task-1",
			prompt:   conveyorv1alpha1.PromptSource{Inline: "hi"},
			inputs:   promptInput(""),
			wantZero: true,
		},
		{
			name:          "inline prompt → owned configmap volume",
			taskName:      "my-task",
			prompt:        conveyorv1alpha1.PromptSource{Inline: "do work"},
			inputs:        promptInput("/task/prompt"),
			wantConfigMap: InlinePromptConfigMapName("my-task"),
			wantMountPath: "/task/prompt",
			wantSubPath:   PromptKey,
		},
		{
			name:     "configMapRef → referenced configmap volume",
			taskName: "task-2",
			prompt: conveyorv1alpha1.PromptSource{
				ConfigMapRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "shared-prompts"},
					Key:                  "review.txt",
				},
			},
			inputs:        promptInput("/agent/prompt"),
			wantConfigMap: "shared-prompts",
			wantMountPath: "/agent/prompt",
			wantSubPath:   "review.txt",
		},
		{
			name:     "secretRef → secret volume",
			taskName: "task-3",
			prompt: conveyorv1alpha1.PromptSource{
				SecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "secret-prompt"},
					Key:                  "body",
				},
			},
			inputs:        promptInput("/task/prompt"),
			wantSecret:    "secret-prompt",
			wantMountPath: "/task/prompt",
			wantSubPath:   "body",
		},
		{
			name:     "no source is an error",
			taskName: "task-4",
			prompt:   conveyorv1alpha1.PromptSource{}, // nothing set
			inputs:   promptInput("/task/prompt"),
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proj, err := BuildPromptProjection(tc.taskName, tc.prompt, tc.inputs)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error when prompt has no source")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantZero {
				if proj.Volume != nil || proj.Mount != nil {
					t.Errorf("expected zero projection, got %+v", proj)
				}
				return
			}
			if proj.Volume == nil || proj.Mount == nil {
				t.Fatal("expected non-nil vol/mount")
			}
			vol, mount := proj.Volume, proj.Mount
			if vol.Name != PromptVolumeName {
				t.Errorf("volume name = %q, want %q", vol.Name, PromptVolumeName)
			}
			if tc.wantConfigMap != "" {
				if vol.ConfigMap == nil {
					t.Fatal("expected ConfigMap volume source")
				}
				if vol.ConfigMap.Name != tc.wantConfigMap {
					t.Errorf("configmap name = %q, want %q", vol.ConfigMap.Name, tc.wantConfigMap)
				}
			}
			if tc.wantSecret != "" {
				if vol.Secret == nil {
					t.Fatal("expected Secret volume source")
				}
				if vol.Secret.SecretName != tc.wantSecret {
					t.Errorf("secret name = %q, want %q", vol.Secret.SecretName, tc.wantSecret)
				}
			}
			if mount.MountPath != tc.wantMountPath {
				t.Errorf("mount path = %q, want %q", mount.MountPath, tc.wantMountPath)
			}
			if mount.SubPath != tc.wantSubPath {
				t.Errorf("subPath = %q, want %q", mount.SubPath, tc.wantSubPath)
			}
			if !mount.ReadOnly {
				t.Error("mount should be read-only")
			}
		})
	}
}

func TestBuildInlinePromptConfigMap(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		prompt   conveyorv1alpha1.PromptSource
		wantNil  bool
		wantData string
	}{
		{
			name:     "inline prompt → configmap with prompt data",
			taskName: "my-task",
			prompt:   conveyorv1alpha1.PromptSource{Inline: "hello world"},
			wantData: "hello world",
		},
		{
			name:     "nil for non-inline prompt",
			taskName: "t",
			prompt: conveyorv1alpha1.PromptSource{
				SecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
					Key:                  "k",
				},
			},
			wantNil: true,
		},
		{
			name:     "nil for empty prompt",
			taskName: "t",
			prompt:   conveyorv1alpha1.PromptSource{},
			wantNil:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := BuildInlinePromptConfigMap(tc.taskName, "ns", tc.prompt)
			if tc.wantNil {
				if cm != nil {
					t.Errorf("expected nil ConfigMap, got %+v", cm)
				}
				return
			}
			if cm == nil {
				t.Fatal("expected non-nil ConfigMap for inline prompt")
			}
			if cm.Name != InlinePromptConfigMapName(tc.taskName) {
				t.Errorf("name = %q, want %q", cm.Name, InlinePromptConfigMapName(tc.taskName))
			}
			if cm.Namespace != "ns" {
				t.Errorf("namespace = %q, want ns", cm.Namespace)
			}
			if cm.Data[PromptKey] != tc.wantData {
				t.Errorf("data[prompt] = %q, want %q", cm.Data[PromptKey], tc.wantData)
			}
			if cm.Labels[LabelManagedBy] != ManagedByValue {
				t.Error("managed-by label missing")
			}
		})
	}
}

func TestInlinePromptConfigMapName(t *testing.T) {
	if got := InlinePromptConfigMapName("my-task"); got != "my-task-prompt" {
		t.Errorf("got %q, want my-task-prompt", got)
	}
}
