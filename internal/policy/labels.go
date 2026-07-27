/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Labels applied to every resource this package generates so the
// reconciler (and kubectl) can find them by selector. Kept here so the
// controller and policy package agree on the same label schema.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelTaskName  = "openconveyor.ai/task"
	ManagedByValue = "conveyor"
	ComponentTask  = "task"
)

// PodSelectorLabels are the labels used for NetworkPolicy.podSelector
// and the pod template itself, so the policy actually binds to the pod.
func PodSelectorLabels(taskName string) map[string]string {
	return map[string]string{
		LabelTaskName: taskName,
	}
}

// OwnershipLabels are applied to every generated resource.
func OwnershipLabels(taskName string) map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelTaskName:  taskName,
	}
}

// ownedMeta is the shared ObjectMeta for every resource this package
// generates on behalf of a Task.
func ownedMeta(name, namespace, taskName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    OwnershipLabels(taskName),
	}
}

// MergeStringSets unions the input slices, drops empties, and returns a
// deduplicated result in insertion order (stable for the first time each
// distinct value appears). Entries are used verbatim — no whitespace
// trimming; egress entries are trimmed separately in resolveEgress.
// Shared by the reconciler when merging Task.spec.permissions.* with
// ClusterAgentClass.spec.requires.* so the effective allowlist is a
// single source of truth.
func MergeStringSets(lists ...[]string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, list := range lists {
		for _, v := range list {
			if v == "" {
				continue
			}
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
