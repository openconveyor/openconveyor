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
				var perm *policy.PermanentError
				if !errors.As(err, &perm) {
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
