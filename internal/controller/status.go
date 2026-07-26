/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/metrics"
)

const (
	conditionReady = "Ready"

	reasonAgentClassMissing = "AgentClassMissing"
	reasonInvalidSpec       = "InvalidSpec"
	reasonPending           = "Pending"
	reasonRunning           = "Running"
	reasonCompleted         = "Completed"
	reasonFailed            = "Failed"
	reasonTimedOut          = "TimedOut"
	reasonEgressResolveFail = "EgressResolveFailed"
)

func (r *TaskReconciler) syncStatus(ctx context.Context, task *conveyorv1alpha1.Task, job *batchv1.Job) error {
	log := logf.FromContext(ctx)
	before := task.Status.DeepCopy()

	task.Status.JobName = job.Name
	task.Status.StartTime = job.Status.StartTime
	task.Status.CompletionTime = job.Status.CompletionTime
	task.Status.ObservedGeneration = task.Generation

	phase, reason, msg := derivePhase(job)
	task.Status.Phase = phase

	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             conditionStatusFor(phase),
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: task.Generation,
	})

	if equalStatus(before, &task.Status) {
		return nil
	}

	if before.Phase != phase {
		log.Info("Task phase transitioned", "from", before.Phase, "to", phase, "reason", reason)
		metrics.ObservePhaseTransition(string(phase), reason)
		if isTerminal(phase) && task.Status.StartTime != nil && task.Status.CompletionTime != nil {
			dur := task.Status.CompletionTime.Sub(task.Status.StartTime.Time).Seconds()
			metrics.ObserveTaskDuration(string(phase), dur)
		}
	}

	return r.Status().Update(ctx, task)
}

func isTerminal(phase conveyorv1alpha1.TaskPhase) bool {
	switch phase {
	case conveyorv1alpha1.TaskPhaseCompleted,
		conveyorv1alpha1.TaskPhaseFailed,
		conveyorv1alpha1.TaskPhaseTimedOut:
		return true
	}
	return false
}

func derivePhase(job *batchv1.Job) (conveyorv1alpha1.TaskPhase, string, string) {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, batchv1.JobSuccessCriteriaMet:
			return conveyorv1alpha1.TaskPhaseCompleted, reasonCompleted, c.Message
		case batchv1.JobFailed:
			if c.Reason == "DeadlineExceeded" {
				return conveyorv1alpha1.TaskPhaseTimedOut, reasonTimedOut, c.Message
			}
			return conveyorv1alpha1.TaskPhaseFailed, reasonFailed, c.Message
		}
	}
	if job.Status.Active > 0 || job.Status.StartTime != nil {
		return conveyorv1alpha1.TaskPhaseRunning, reasonRunning, "Job is running"
	}
	return conveyorv1alpha1.TaskPhasePending, reasonPending, "Waiting for pod to start"
}

func conditionStatusFor(phase conveyorv1alpha1.TaskPhase) metav1.ConditionStatus {
	switch phase {
	case conveyorv1alpha1.TaskPhaseCompleted:
		return metav1.ConditionTrue
	case conveyorv1alpha1.TaskPhaseFailed, conveyorv1alpha1.TaskPhaseTimedOut:
		return metav1.ConditionFalse
	default:
		return metav1.ConditionUnknown
	}
}

// failTask marks the Task Failed because of cause (a spec problem
// retrying cannot fix). When the status write itself fails, both errors
// are joined so the original cause is never masked.
func (r *TaskReconciler) failTask(ctx context.Context, task *conveyorv1alpha1.Task, reason string, cause error) error {
	if err := r.markInvalid(ctx, task, reason, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

func (r *TaskReconciler) markInvalid(ctx context.Context, task *conveyorv1alpha1.Task, reason, msg string) error {
	prevPhase := task.Status.Phase
	task.Status.Phase = conveyorv1alpha1.TaskPhaseFailed
	task.Status.ObservedGeneration = task.Generation
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: task.Generation,
	})
	if prevPhase != conveyorv1alpha1.TaskPhaseFailed {
		metrics.ObservePhaseTransition(string(conveyorv1alpha1.TaskPhaseFailed), reason)
	}
	return r.Status().Update(ctx, task)
}

// markWaitingForAgentClass records that the Task is waiting for its
// ClusterAgentClass to be created. Unlike markInvalid this keeps the
// Task in Pending so it picks up where it left off once the class
// exists. The status update is skipped when nothing changed so the
// periodic requeue doesn't spam the API server.
func (r *TaskReconciler) markWaitingForAgentClass(ctx context.Context, task *conveyorv1alpha1.Task) error {
	before := task.Status.DeepCopy()
	task.Status.Phase = conveyorv1alpha1.TaskPhasePending
	task.Status.ObservedGeneration = task.Generation
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionUnknown,
		Reason:             reasonAgentClassMissing,
		Message:            fmt.Sprintf("Waiting for ClusterAgentClass %q to be created", task.Spec.Agent.Ref),
		ObservedGeneration: task.Generation,
	})
	if equalStatus(before, &task.Status) {
		return nil
	}
	return r.Status().Update(ctx, task)
}

// equalStatus reports whether two statuses are semantically equal,
// ignoring condition order and LastTransitionTime (which only moves
// when something else changed anyway).
func equalStatus(a, b *conveyorv1alpha1.TaskStatus) bool {
	if a.Phase != b.Phase || a.JobName != b.JobName {
		return false
	}
	if a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if !timesEqual(a.StartTime, b.StartTime) || !timesEqual(a.CompletionTime, b.CompletionTime) {
		return false
	}
	if len(a.Conditions) != len(b.Conditions) {
		return false
	}
	for _, ac := range a.Conditions {
		bc := meta.FindStatusCondition(b.Conditions, ac.Type)
		if bc == nil || ac.Status != bc.Status || ac.Reason != bc.Reason || ac.Message != bc.Message ||
			ac.ObservedGeneration != bc.ObservedGeneration {
			return false
		}
	}
	return true
}

func timesEqual(a, b *metav1.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Equal(b)
	}
}
