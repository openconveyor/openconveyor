/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const (
	conditionResult = "Result"

	reasonResultRecorded    = "Recorded"
	reasonResultNotProvided = "NotProvided"
	reasonResultUnavailable = "Unavailable"
	reasonResultInvalid     = "Invalid"

	// terminationMessagePath is the file that the agent writes its Result
	// to. It is the Kubernetes default, set explicitly in buildJob.
	terminationMessagePath = "/dev/termination-log"

	// DefaultResultWait is how long the controller looks for the agent pod
	// of a terminal Task before it records Unavailable. It covers the
	// informer lag between the Job and the Pod cache, and the 30 s SIGTERM
	// grace period of a pod that the Job controller deletes on timeout.
	DefaultResultWait = 2 * time.Minute

	// resultRequeue is the interval at which the controller looks again for
	// the agent pod of a terminal Task. The Pod watch is the primary wake-up
	// signal; the requeue is a safety net.
	resultRequeue = 5 * time.Second
)

// resultDecided reports whether the controller already recorded the Result
// condition. The termination message of a pod with restartPolicy Never does
// not change after the container ends, so the controller reads it once.
func resultDecided(task *conveyorv1alpha1.Task) bool {
	return meta.FindStatusCondition(task.Status.Conditions, conditionResult) != nil
}

// observeResult reads the termination message of the agent container and
// records the Result on task.Status. It does not write the status; the
// caller does. It returns the delay after which the caller must reconcile
// again, or zero.
//
// The function accepts only a pod that the Job with jobUID controls. Thus
// a pod that someone else labels with the Task name cannot inject a Result.
// While the Task is not terminal, a missing or running pod is normal and
// the function does nothing. When the Task is terminal, it requeues until
// the pod ends, or records Unavailable after resultWait.
func (r *TaskReconciler) observeResult(
	ctx context.Context,
	task *conveyorv1alpha1.Task,
	jobUID types.UID,
	egress []string,
) (time.Duration, error) {
	if resultDecided(task) {
		return 0, nil
	}

	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(task.Namespace),
		client.MatchingLabels{policy.LabelTaskName: task.Name}); err != nil {
		return 0, fmt.Errorf("list task pods: %w", err)
	}

	podSeen := false
	for i := range pods.Items {
		pod := &pods.Items[i]
		owner := metav1.GetControllerOf(pod)
		if owner == nil || owner.Kind != "Job" || owner.UID != jobUID {
			continue
		}
		podSeen = true
		if msg, ok := agentTerminationMessage(pod); ok {
			r.applyParsedResult(ctx, task, policy.ParseResult(msg, egress))
			return 0, nil
		}
	}

	if !isTerminal(task.Status.Phase) {
		return 0, nil
	}
	if time.Since(terminalSince(task)) < r.resultWait() {
		return resultRequeue, nil
	}
	msg := "The agent pod was gone before the controller read its termination message"
	if podSeen {
		msg = "The agent container did not terminate before the controller stopped to wait"
	}
	setResultCondition(task, reasonResultUnavailable, msg)
	return 0, nil
}

// agentTerminationMessage returns the termination message of the agent
// container, and false while the container has not terminated.
func agentTerminationMessage(pod *corev1.Pod) (string, bool) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != agentContainerName || cs.State.Terminated == nil {
			continue
		}
		return cs.State.Terminated.Message, true
	}
	return "", false
}

// applyParsedResult copies a parsed Result onto the status. The condition
// message is a fixed sentence from the parser. The controller never copies
// the raw termination message into a condition, an Event or a log line.
func (r *TaskReconciler) applyParsedResult(ctx context.Context, task *conveyorv1alpha1.Task, parsed policy.ParsedResult) {
	task.Status.Result = parsed.Result
	switch parsed.Outcome {
	case policy.ResultRecorded:
		setResultCondition(task, reasonResultRecorded, "The agent reported a Result")
	case policy.ResultInvalid:
		setResultCondition(task, reasonResultInvalid, parsed.Problem)
	default:
		setResultCondition(task, reasonResultNotProvided, parsed.Problem)
	}
	links := 0
	if parsed.Result != nil {
		links = len(parsed.Result.Links)
	}
	logf.FromContext(ctx).Info("Read Task result", "outcome", parsed.Outcome,
		"links", links, "droppedLinks", parsed.DroppedLinks)
}

func setResultCondition(task *conveyorv1alpha1.Task, reason, msg string) {
	status := metav1.ConditionFalse
	if reason == reasonResultRecorded {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&task.Status.Conditions, metav1.Condition{
		Type:               conditionResult,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: task.Generation,
	})
}

// terminalSince is the time at which the Task became terminal: the last
// transition of the Ready condition, or now when the condition has no time.
func terminalSince(task *conveyorv1alpha1.Task) time.Time {
	if c := meta.FindStatusCondition(task.Status.Conditions, conditionReady); c != nil && !c.LastTransitionTime.IsZero() {
		return c.LastTransitionTime.Time
	}
	return time.Now()
}

func (r *TaskReconciler) resultWait() time.Duration {
	if r.ResultWait > 0 {
		return r.ResultWait
	}
	return DefaultResultWait
}

// recordTerminalResult completes the Result of a terminal Task. The Job
// usually reports its condition before the Pod cache has the terminated
// container, so the Result often arrives after the phase. A Task that never
// ran a Job (InvalidSpec, EgressResolveFailed) gets no Result condition.
func (r *TaskReconciler) recordTerminalResult(ctx context.Context, task *conveyorv1alpha1.Task) (time.Duration, error) {
	if resultDecided(task) || task.Status.JobName == "" {
		return 0, nil
	}
	before := task.Status.DeepCopy()

	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{Name: task.Status.JobName, Namespace: task.Namespace}, &job)
	var requeue time.Duration
	switch {
	case apierrors.IsNotFound(err):
		// The Job and its pod no longer exist (Job TTL, a manual delete).
		// Nothing can prove that a pod belonged to this Task any more.
		setResultCondition(task, reasonResultUnavailable,
			"The agent pod was gone before the controller read its termination message")
	case err != nil:
		return 0, fmt.Errorf("get job: %w", err)
	default:
		requeue, err = r.observeResult(ctx, task, job.UID, r.terminalEgress(ctx, task))
		if err != nil {
			return 0, err
		}
	}

	if equalStatus(before, &task.Status) {
		return requeue, nil
	}
	return requeue, r.Status().Update(ctx, task)
}

// terminalEgress returns the effective egress allowlist of a terminal Task.
// When its ClusterAgentClass no longer exists, it returns the Task entries
// only. That list is narrower, so the fallback never accepts more links.
func (r *TaskReconciler) terminalEgress(ctx context.Context, task *conveyorv1alpha1.Task) []string {
	var agent conveyorv1alpha1.ClusterAgentClass
	if err := r.Get(ctx, types.NamespacedName{Name: task.Spec.Agent.Ref}, &agent); err != nil {
		return policy.MergeStringSets(task.Spec.Permissions.Egress)
	}
	return policy.MergeStringSets(task.Spec.Permissions.Egress, agent.Spec.Requires.Egress)
}

// taskForPod maps a Pod event to the Task named in its openconveyor.ai/task
// label. The Job owns the pod, so Owns(&batchv1.Job{}) does not reach the
// Task; this map function closes that gap.
func taskForPod(_ context.Context, obj client.Object) []reconcile.Request {
	labels := obj.GetLabels()
	name := labels[policy.LabelTaskName]
	if name == "" || labels[policy.LabelManagedBy] != policy.ManagedByValue {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: name, Namespace: obj.GetNamespace()}}}
}

// minRequeue merges two requeue delays and keeps the shorter non-zero one.
func minRequeue(res ctrl.Result, d time.Duration) ctrl.Result {
	if d > 0 && (res.RequeueAfter == 0 || d < res.RequeueAfter) {
		res.RequeueAfter = d
	}
	return res
}
