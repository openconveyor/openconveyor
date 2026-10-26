/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// validateTask is defense in depth behind the CRD CEL rules. It still guards
// objects that never passed the current admission rules (stored Tasks from
// before the rules, ratcheted updates), so this test calls it directly.
func TestValidateTask(t *testing.T) {
	valid := func() *conveyorv1alpha1.Task {
		return &conveyorv1alpha1.Task{
			Spec: conveyorv1alpha1.TaskSpec{
				Agent:  conveyorv1alpha1.AgentRef{Ref: "hello"},
				Prompt: conveyorv1alpha1.PromptSource{Inline: "hi"},
				Resources: conveyorv1alpha1.TaskResources{
					Timeout: metav1.Duration{Duration: time.Minute},
				},
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*conveyorv1alpha1.Task)
		wantErr string
	}{
		{
			name:   "valid spec",
			mutate: func(*conveyorv1alpha1.Task) {},
		},
		{
			name: "zero timeout",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Resources.Timeout = metav1.Duration{}
			},
			wantErr: "spec.resources.timeout",
		},
		{
			name: "negative timeout",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Resources.Timeout = metav1.Duration{Duration: -time.Second}
			},
			wantErr: "spec.resources.timeout",
		},
		{
			name: "empty agent ref",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Agent.Ref = ""
			},
			wantErr: "spec.agent.ref",
		},
		{
			name: "no prompt source",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Prompt = conveyorv1alpha1.PromptSource{}
			},
			wantErr: "exactly one",
		},
		{
			name: "two prompt sources",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Prompt.ConfigMapRef = &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "prompts"},
					Key:                  "p",
				}
			},
			wantErr: "exactly one",
		},
		{
			name: "malformed cpu",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Resources.CPU = "not-a-cpu"
			},
			wantErr: "spec.resources.cpu",
		},
		{
			name: "malformed memory",
			mutate: func(task *conveyorv1alpha1.Task) {
				task.Spec.Resources.Memory = "10Gigabytes"
			},
			wantErr: "spec.resources.memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			task := valid()
			tt.mutate(task)
			err := validateTask(task)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateTask() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateTask() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateTask() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
