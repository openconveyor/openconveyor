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
	"time"

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
	reasonSuspended         = "Suspended"
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
		r.recordEvent(task, eventTypeFor(phase), reason, msg)
		if isTerminal(phase) && task.Status.StartTime != nil && task.Status.CompletionTime != nil {
			dur := task.Status.CompletionTime.Sub(task.Status.StartTime.Time).Seconds()
			metrics.ObserveTaskDuration(string(phase), dur)
		}
	}

	return r.Status().Update(ctx, task)
}

// eventActionReconcile is the events.k8s.io/v1 `action` on every Event
// the controller emits: each one reports what a reconcile did to the
// Task.
const eventActionReconcile = "Reconcile"

// recordEvent publishes an Event on the Task when a Recorder is present.
// Messages follow the logging style guide: a capital first letter and no
// trailing period.
func (r *TaskReconciler) recordEvent(task *conveyorv1alpha1.Task, eventType, reason, msg string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(task, nil, eventType, reason, eventActionReconcile, "%s", msg)
}

// eventTypeFor maps a phase to the Event type: the failed terminal phases
// (Failed, TimedOut) are Warning, every other phase is Normal.
func eventTypeFor(phase conveyorv1alpha1.TaskPhase) string {
	switch phase {
	case conveyorv1alpha1.TaskPhaseFailed, conveyorv1alpha1.TaskPhaseTimedOut:
		return corev1.EventTypeWarning
	default:
		return corev1.EventTypeNormal
	}
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

// derivePhase maps the Job's state to a Task phase, a condition reason
// and a message. The Job controller leaves the condition message empty
// for the common cases, so each branch falls back to a fixed sentence.
// The same text goes into the Ready condition and into the Event.
func derivePhase(job *batchv1.Job) (conveyorv1alpha1.TaskPhase, string, string) {
	for _, c := range job.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, batchv1.JobSuccessCriteriaMet:
			return conveyorv1alpha1.TaskPhaseCompleted, reasonCompleted, messageOr(c.Message, "Job completed")
		case batchv1.JobFailed:
			if c.Reason == batchv1.JobReasonDeadlineExceeded {
				return conveyorv1alpha1.TaskPhaseTimedOut, reasonTimedOut, messageOr(c.Message, "Job exceeded its deadline")
			}
			return conveyorv1alpha1.TaskPhaseFailed, reasonFailed, messageOr(c.Message, "Job failed")
		case batchv1.JobSuspended:
			// A suspended Job has a StartTime and would otherwise read as
			// Running. Report Pending, so consumers do not think that work is
			// active.
			return conveyorv1alpha1.TaskPhasePending, reasonSuspended, messageOr(c.Message, "Job is suspended")
		}
	}
	if job.Status.Active > 0 || job.Status.StartTime != nil {
		return conveyorv1alpha1.TaskPhaseRunning, reasonRunning, "Job is running"
	}
	return conveyorv1alpha1.TaskPhasePending, reasonPending, "Waiting for pod to start"
}

// messageOr returns msg, or fallback when the Job left it empty.
func messageOr(msg, fallback string) string {
	if msg == "" {
		return fallback
	}
	return msg
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

// failTask marks the Task Failed because of cause, a spec problem that a
// retry cannot fix. When the status write itself fails, it joins both
// errors, so the result keeps the original cause.
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
		r.recordEvent(task, corev1.EventTypeWarning, reason, invalidEventMessage(reason, msg))
	}
	return r.Status().Update(ctx, task)
}

// invalidEventMessage prefixes an error string with a sentence that
// names what went wrong. The Event then reads well although the error
// text starts in lowercase.
func invalidEventMessage(reason, msg string) string {
	lead := "Task spec is invalid"
	if reason == reasonEgressResolveFail {
		lead = "Egress allowlist could not be resolved"
	}
	return lead + ": " + msg
}

// markWaitingForAgentClass records that the Task waits for someone to
// create its ClusterAgentClass. Unlike markInvalid, it keeps the Task in
// Pending, so the Task continues where it stopped when the class exists.
// It skips the status update when nothing changed, so the periodic
// requeue does not flood the API server.
func (r *TaskReconciler) markWaitingForAgentClass(ctx context.Context, task *conveyorv1alpha1.Task) error {
	before := task.Status.DeepCopy()
	msg := fmt.Sprintf("Waiting for ClusterAgentClass %q to be created", task.Spec.Agent.Ref)
	task.Status.Phase = conveyorv1alpha1.TaskPhasePending
	task.Status.ObservedGeneration = task.Generation
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionUnknown,
		Reason:             reasonAgentClassMissing,
		Message:            msg,
		ObservedGeneration: task.Generation,
	})
	if equalStatus(before, &task.Status) {
		return nil
	}
	// A Task that cannot start until someone creates the class needs
	// action, so the Event type is Warning rather than Normal.
	r.recordEvent(task, corev1.EventTypeWarning, reasonAgentClassMissing, msg)
	return r.Status().Update(ctx, task)
}

// deletionDeadline returns the time at which a terminal Task becomes
// eligible for garbage collection. The deadline is the finish time plus
// spec.ttlSecondsAfterFinished, or plus defaultTTL when the spec leaves it
// unset. The finish time is status.completionTime, which comes from the
// Job. For a Task that never ran a Job, or whose Job reported no
// completion time, the finish time is the last transition of the Ready
// condition. ok is false when there is no TTL or no usable timestamp.
func deletionDeadline(task *conveyorv1alpha1.Task, defaultTTL *int32) (time.Time, bool) {
	ttl := task.Spec.TTLSecondsAfterFinished
	if ttl == nil {
		ttl = defaultTTL
	}
	if ttl == nil {
		return time.Time{}, false
	}
	var finished *metav1.Time
	switch {
	case task.Status.CompletionTime != nil:
		finished = task.Status.CompletionTime
	default:
		if c := meta.FindStatusCondition(task.Status.Conditions, conditionReady); c != nil && !c.LastTransitionTime.IsZero() {
			finished = &c.LastTransitionTime
		}
	}
	if finished == nil {
		return time.Time{}, false
	}
	return finished.Add(time.Duration(*ttl) * time.Second), true
}

// equalStatus reports whether two statuses are semantically equal. It
// ignores the condition order and LastTransitionTime, which only moves
// when something else changed.
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
