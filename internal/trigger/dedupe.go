/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

// stampTriggerClass sets the trigger-class label on a Task that the adapter
// built from ctc. The label overrides a mapping with the same key, so a
// payload cannot move a Task into another class. A class name that is not a
// valid label value gets no label; the CRD rejects such a name together with
// spec.dedupe.
func stampTriggerClass(task *conveyorv1alpha1.Task, ctc *conveyorv1alpha1.ClusterTriggerClass) {
	if len(validation.IsValidLabelValue(ctc.Name)) > 0 {
		return
	}
	if task.Labels == nil {
		task.Labels = map[string]string{}
	}
	task.Labels[policy.LabelTriggerClass] = ctc.Name
}

// findActiveDuplicate returns the name of a Task that blocks task under the
// dedupe rule of ctc (ADR-0009), or "" when nothing blocks it.
//
// A Task blocks when it is in the same namespace, carries the same
// trigger-class label and the same value for every spec.dedupe.uniqueBy
// key, and is not terminal. An empty phase counts as active. A Task that
// the API server deletes does not block. The list goes through reader, a
// live API reader, so a Task that the adapter created a moment ago is
// visible. The check is best effort: two events within one round trip can
// both pass.
//
// When the built Task misses one of the uniqueBy labels (a mapping did not
// resolve), the function skips the check, and the event creates a Task.
func (h *Handler) findActiveDuplicate(
	ctx context.Context,
	ctc *conveyorv1alpha1.ClusterTriggerClass,
	task *conveyorv1alpha1.Task,
) (string, error) {
	if ctc.Spec.Dedupe == nil || len(ctc.Spec.Dedupe.UniqueBy) == 0 {
		return "", nil
	}
	selector := client.MatchingLabels{}
	if v := task.Labels[policy.LabelTriggerClass]; v != "" {
		selector[policy.LabelTriggerClass] = v
	} else {
		h.Log.V(1).Info("Skipped dedupe because the class name is not a label value", "triggerClass", ctc.Name)
		return "", nil
	}
	for _, key := range ctc.Spec.Dedupe.UniqueBy {
		v := task.Labels[key]
		if v == "" {
			h.Log.V(1).Info("Skipped dedupe because a uniqueBy label is missing", "triggerClass", ctc.Name, "label", key)
			return "", nil
		}
		selector[key] = v
	}

	var tasks conveyorv1alpha1.TaskList
	if err := h.reader().List(ctx, &tasks, client.InNamespace(task.Namespace), selector); err != nil {
		return "", fmt.Errorf("list tasks for dedupe: %w", err)
	}
	for i := range tasks.Items {
		t := &tasks.Items[i]
		if t.DeletionTimestamp != nil || isTerminalPhase(t.Status.Phase) {
			continue
		}
		return t.Name, nil
	}
	return "", nil
}

// reader returns the live API reader for the dedupe list, or the client
// when the caller set no reader (unit tests with a fake client).
func (h *Handler) reader() client.Reader {
	if h.Reader != nil {
		return h.Reader
	}
	return h.Client
}

// isTerminalPhase mirrors the Terminal Task rule of the controller:
// Completed, Failed and TimedOut never run again.
func isTerminalPhase(phase conveyorv1alpha1.TaskPhase) bool {
	switch phase {
	case conveyorv1alpha1.TaskPhaseCompleted, conveyorv1alpha1.TaskPhaseFailed, conveyorv1alpha1.TaskPhaseTimedOut:
		return true
	}
	return false
}
