/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
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

// jobWithCondition builds a Job whose status carries a single condition.
func jobWithCondition(condType batchv1.JobConditionType, status corev1.ConditionStatus, reason string) *batchv1.Job {
	return &batchv1.Job{
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{Type: condType, Status: status, Reason: reason}},
		},
	}
}

func TestDerivePhase(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name       string
		job        *batchv1.Job
		wantPhase  conveyorv1alpha1.TaskPhase
		wantReason string
	}{
		{
			name:       "JobComplete → Completed",
			job:        jobWithCondition(batchv1.JobComplete, corev1.ConditionTrue, ""),
			wantPhase:  conveyorv1alpha1.TaskPhaseCompleted,
			wantReason: reasonCompleted,
		},
		{
			name:       "JobSuccessCriteriaMet → Completed",
			job:        jobWithCondition(batchv1.JobSuccessCriteriaMet, corev1.ConditionTrue, ""),
			wantPhase:  conveyorv1alpha1.TaskPhaseCompleted,
			wantReason: reasonCompleted,
		},
		{
			name:       "JobFailed → Failed",
			job:        jobWithCondition(batchv1.JobFailed, corev1.ConditionTrue, "BackoffLimitExceeded"),
			wantPhase:  conveyorv1alpha1.TaskPhaseFailed,
			wantReason: reasonFailed,
		},
		{
			name:       "JobFailed with DeadlineExceeded → TimedOut",
			job:        jobWithCondition(batchv1.JobFailed, corev1.ConditionTrue, batchv1.JobReasonDeadlineExceeded),
			wantPhase:  conveyorv1alpha1.TaskPhaseTimedOut,
			wantReason: reasonTimedOut,
		},
		{
			name:       "condition with Status=False is skipped",
			job:        jobWithCondition(batchv1.JobFailed, corev1.ConditionFalse, "BackoffLimitExceeded"),
			wantPhase:  conveyorv1alpha1.TaskPhasePending,
			wantReason: reasonPending,
		},
		{
			name:       "active pods → Running",
			job:        &batchv1.Job{Status: batchv1.JobStatus{Active: 1}},
			wantPhase:  conveyorv1alpha1.TaskPhaseRunning,
			wantReason: reasonRunning,
		},
		{
			name:       "start time set → Running",
			job:        &batchv1.Job{Status: batchv1.JobStatus{StartTime: &now}},
			wantPhase:  conveyorv1alpha1.TaskPhaseRunning,
			wantReason: reasonRunning,
		},
		{
			name:       "empty status → Pending",
			job:        &batchv1.Job{},
			wantPhase:  conveyorv1alpha1.TaskPhasePending,
			wantReason: reasonPending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase, reason, _ := derivePhase(tt.job)
			if phase != tt.wantPhase {
				t.Errorf("derivePhase() phase = %v, want %v", phase, tt.wantPhase)
			}
			if reason != tt.wantReason {
				t.Errorf("derivePhase() reason = %v, want %v", reason, tt.wantReason)
			}
		})
	}
}

func TestConditionStatusFor(t *testing.T) {
	tests := []struct {
		phase conveyorv1alpha1.TaskPhase
		want  metav1.ConditionStatus
	}{
		{conveyorv1alpha1.TaskPhasePending, metav1.ConditionUnknown},
		{conveyorv1alpha1.TaskPhaseRunning, metav1.ConditionUnknown},
		{conveyorv1alpha1.TaskPhaseCompleted, metav1.ConditionTrue},
		{conveyorv1alpha1.TaskPhaseFailed, metav1.ConditionFalse},
		{conveyorv1alpha1.TaskPhaseTimedOut, metav1.ConditionFalse},
	}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			if got := conditionStatusFor(tt.phase); got != tt.want {
				t.Errorf("conditionStatusFor(%v) = %v, want %v", tt.phase, got, tt.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		phase conveyorv1alpha1.TaskPhase
		want  bool
	}{
		{conveyorv1alpha1.TaskPhasePending, false},
		{conveyorv1alpha1.TaskPhaseRunning, false},
		{conveyorv1alpha1.TaskPhaseCompleted, true},
		{conveyorv1alpha1.TaskPhaseFailed, true},
		{conveyorv1alpha1.TaskPhaseTimedOut, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			if got := isTerminal(tt.phase); got != tt.want {
				t.Errorf("isTerminal(%v) = %v, want %v", tt.phase, got, tt.want)
			}
		})
	}
}

func TestTimesEqual(t *testing.T) {
	t1 := metav1.NewTime(metav1.Now().Truncate(0))
	t1Copy := t1
	t2 := metav1.NewTime(t1.Add(1))

	tests := []struct {
		name string
		a, b *metav1.Time
		want bool
	}{
		{"both nil", nil, nil, true},
		{"a nil", nil, &t1, false},
		{"b nil", &t1, nil, false},
		{"equal", &t1, &t1Copy, true},
		{"different", &t1, &t2, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timesEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("timesEqual() = %v, want %v", got, tt.want)
			}
		})
	}
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
