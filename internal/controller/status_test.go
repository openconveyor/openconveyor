/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func readyCondition(gen int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionUnknown,
		Reason:             reasonRunning,
		Message:            "Job is running",
		ObservedGeneration: gen,
	}
}

func TestEqualStatus(t *testing.T) {
	base := func() *conveyorv1alpha1.TaskStatus {
		return &conveyorv1alpha1.TaskStatus{
			Phase:              conveyorv1alpha1.TaskPhaseRunning,
			JobName:            "job",
			ObservedGeneration: 1,
			Conditions:         []metav1.Condition{readyCondition(1)},
		}
	}

	tests := []struct {
		name   string
		mutate func(*conveyorv1alpha1.TaskStatus)
		want   bool
	}{
		{"identical", func(*conveyorv1alpha1.TaskStatus) {}, true},
		{"phase differs", func(s *conveyorv1alpha1.TaskStatus) {
			s.Phase = conveyorv1alpha1.TaskPhaseFailed
		}, false},
		{"status observedGeneration differs", func(s *conveyorv1alpha1.TaskStatus) {
			s.ObservedGeneration = 2
		}, false},
		// Regression: the old hand-rolled compare ignored the condition's
		// ObservedGeneration, so a generation-only bump never reached the
		// API server and consumers saw a stale condition.
		{"condition observedGeneration differs", func(s *conveyorv1alpha1.TaskStatus) {
			s.Conditions[0].ObservedGeneration = 2
		}, false},
		{"condition message differs", func(s *conveyorv1alpha1.TaskStatus) {
			s.Conditions[0].Message = "changed"
		}, false},
		{"lastTransitionTime ignored", func(s *conveyorv1alpha1.TaskStatus) {
			s.Conditions[0].LastTransitionTime = metav1.Now()
		}, true},
		{"condition order ignored", func(s *conveyorv1alpha1.TaskStatus) {
			extra := metav1.Condition{Type: "Extra", Status: metav1.ConditionTrue, Reason: "X"}
			s.Conditions = []metav1.Condition{extra, s.Conditions[0]}
		}, false}, // length differs from base; see reorder case below
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := base(), base()
			tt.mutate(b)
			if got := equalStatus(a, b); got != tt.want {
				t.Errorf("equalStatus() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("reorder with equal content", func(t *testing.T) {
		extra := metav1.Condition{Type: "Extra", Status: metav1.ConditionTrue, Reason: "X"}
		a, b := base(), base()
		a.Conditions = append(a.Conditions, extra)
		b.Conditions = []metav1.Condition{extra, b.Conditions[0]}
		if !equalStatus(a, b) {
			t.Error("equalStatus() = false for reordered but equal conditions, want true")
		}
	})
}

// TestSetStatusConditionRefreshesObservedGeneration pins the behavior the
// old hand-rolled setCondition got wrong: an update that changes only
// ObservedGeneration must still land on the stored condition.
func TestSetStatusConditionRefreshesObservedGeneration(t *testing.T) {
	conds := []metav1.Condition{readyCondition(1)}
	meta.SetStatusCondition(&conds, readyCondition(2))
	got := meta.FindStatusCondition(conds, conditionReady)
	if got == nil {
		t.Fatal("condition disappeared")
	}
	if got.ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %d, want 2", got.ObservedGeneration)
	}
}
