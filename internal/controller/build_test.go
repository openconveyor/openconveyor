/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

// TestBuildJob_SecurityDefaults locks every hardening flag the security
// model calls a guarantee, not a knob (docs/security-model.md), against
// regression — independent of envtest.
func TestBuildJob_SecurityDefaults(t *testing.T) {
	task := &conveyorv1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "sec-task", Namespace: "ns"},
		Spec: conveyorv1alpha1.TaskSpec{
			Agent:  conveyorv1alpha1.AgentRef{Ref: "hello"},
			Prompt: conveyorv1alpha1.PromptSource{Inline: "go"},
			Resources: conveyorv1alpha1.TaskResources{
				Timeout: metav1.Duration{Duration: 5 * time.Minute},
			},
		},
	}
	agent := &conveyorv1alpha1.ClusterAgentClass{
		ObjectMeta: metav1.ObjectMeta{Name: "hello"},
		Spec:       conveyorv1alpha1.ClusterAgentClassSpec{Image: "busybox:1.36"},
	}

	job, err := buildJob(task, agent, nil)
	if err != nil {
		t.Fatalf("buildJob() = %v, want nil", err)
	}

	if got := *job.Spec.BackoffLimit; got != 0 {
		t.Errorf("BackoffLimit = %d, want 0 (no blind retries)", got)
	}
	if got := *job.Spec.ActiveDeadlineSeconds; got != 300 {
		t.Errorf("ActiveDeadlineSeconds = %d, want 300 (from spec.resources.timeout)", got)
	}
	if job.Spec.TTLSecondsAfterFinished == nil {
		t.Error("TTLSecondsAfterFinished not set")
	}

	pod := job.Spec.Template.Spec
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %v, want Never", pod.RestartPolicy)
	}
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Error("AutomountServiceAccountToken must be false without declared RBAC")
	}

	if len(pod.Containers) != 1 {
		t.Fatalf("container count = %d, want 1", len(pod.Containers))
	}
	sc := pod.Containers[0].SecurityContext
	if sc == nil {
		t.Fatal("SecurityContext not set")
	}
	checks := []struct {
		name string
		got  *bool
		want bool
	}{
		{"RunAsNonRoot", sc.RunAsNonRoot, true},
		{"ReadOnlyRootFilesystem", sc.ReadOnlyRootFilesystem, true},
		{"AllowPrivilegeEscalation", sc.AllowPrivilegeEscalation, false},
	}
	for _, c := range checks {
		if c.got == nil || *c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if sc.RunAsUser == nil || *sc.RunAsUser != nonRootUID {
		t.Errorf("RunAsUser = %v, want %d", sc.RunAsUser, nonRootUID)
	}
	if sc.RunAsGroup == nil || *sc.RunAsGroup != nonRootUID {
		t.Errorf("RunAsGroup = %v, want %d", sc.RunAsGroup, nonRootUID)
	}
	if sc.Capabilities == nil || len(sc.Capabilities.Drop) != 1 || sc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("Capabilities.Drop = %v, want [ALL]", sc.Capabilities)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("SeccompProfile = %v, want RuntimeDefault", sc.SeccompProfile)
	}

	var scratch *corev1.VolumeMount
	for i, m := range pod.Containers[0].VolumeMounts {
		if m.Name == scratchVolumeName {
			scratch = &pod.Containers[0].VolumeMounts[i]
		}
	}
	if scratch == nil || scratch.MountPath != "/tmp" {
		t.Errorf("scratch mount = %+v, want emptyDir at /tmp", scratch)
	}
}

// TestBuildJob_RBACMountsToken pins the automount flip side: declaring
// any RBAC rule mounts the ServiceAccount token.
func TestBuildJob_RBACMountsToken(t *testing.T) {
	task := &conveyorv1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "rbac-task", Namespace: "ns"},
		Spec: conveyorv1alpha1.TaskSpec{
			Agent:  conveyorv1alpha1.AgentRef{Ref: "hello"},
			Prompt: conveyorv1alpha1.PromptSource{Inline: "go"},
			Resources: conveyorv1alpha1.TaskResources{
				Timeout: metav1.Duration{Duration: time.Minute},
			},
			Permissions: conveyorv1alpha1.Permissions{
				RBAC: []conveyorv1alpha1.RBACRule{
					{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
				},
			},
		},
	}
	agent := &conveyorv1alpha1.ClusterAgentClass{
		ObjectMeta: metav1.ObjectMeta{Name: "hello"},
		Spec:       conveyorv1alpha1.ClusterAgentClassSpec{Image: "busybox:1.36"},
	}

	job, err := buildJob(task, agent, nil)
	if err != nil {
		t.Fatalf("buildJob() = %v, want nil", err)
	}
	automount := job.Spec.Template.Spec.AutomountServiceAccountToken
	if automount == nil || !*automount {
		t.Error("AutomountServiceAccountToken must be true when RBAC is declared")
	}
}

func TestBuildResourceRequirements(t *testing.T) {
	timeout := metav1.Duration{Duration: time.Minute}

	tests := []struct {
		name      string
		resources conveyorv1alpha1.TaskResources
		want      corev1.ResourceList // nil means no requests/limits at all
		wantErr   string
	}{
		{
			name:      "no cpu or memory",
			resources: conveyorv1alpha1.TaskResources{Timeout: timeout},
		},
		{
			name:      "cpu only",
			resources: conveyorv1alpha1.TaskResources{CPU: "500m", Timeout: timeout},
			want:      corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
		},
		{
			name:      "memory only",
			resources: conveyorv1alpha1.TaskResources{Memory: "512Mi", Timeout: timeout},
			want:      corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
		},
		{
			name:      "cpu and memory",
			resources: conveyorv1alpha1.TaskResources{CPU: "1", Memory: "1Gi", Timeout: timeout},
			want: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1"),
				corev1.ResourceMemory: resource.MustParse("1Gi"),
			},
		},
		{
			name:      "malformed cpu",
			resources: conveyorv1alpha1.TaskResources{CPU: "not-a-cpu", Timeout: timeout},
			wantErr:   "spec.resources.cpu",
		},
		{
			name:      "malformed memory",
			resources: conveyorv1alpha1.TaskResources{Memory: "10Gigabytes", Timeout: timeout},
			wantErr:   "spec.resources.memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildResourceRequirements(tt.resources)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("buildResourceRequirements() = nil error, want error containing %q", tt.wantErr)
				}
				if _, ok := errors.AsType[*policy.PermanentError](err); !ok {
					t.Fatalf("buildResourceRequirements() error %v is not a *policy.PermanentError", err)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("buildResourceRequirements() = %v, want error containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("buildResourceRequirements() = %v, want nil", err)
			}
			if tt.want == nil {
				if got.Requests != nil || got.Limits != nil {
					t.Fatalf("buildResourceRequirements() = %+v, want empty requirements", got)
				}
				return
			}
			for name, want := range tt.want {
				if req := got.Requests[name]; req.Cmp(want) != 0 {
					t.Errorf("Requests[%s] = %s, want %s", name, req.String(), want.String())
				}
				if lim := got.Limits[name]; lim.Cmp(want) != 0 {
					t.Errorf("Limits[%s] = %s, want %s", name, lim.String(), want.String())
				}
			}
			if len(got.Requests) != len(tt.want) || len(got.Limits) != len(tt.want) {
				t.Errorf("requests/limits sizes = %d/%d, want %d", len(got.Requests), len(got.Limits), len(tt.want))
			}
		})
	}
}
