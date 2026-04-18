/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

// Labels applied to every resource this package generates so the
// reconciler (and kubectl) can find them by selector. Kept here so the
// controller and policy package agree on the same label schema.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelTaskName  = "openconveyor.ai/task"
	ManagedByValue = "conveyor"
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

// MergeStringSets unions the input slices, trims whitespace, drops empties,
// and returns a deduplicated result in insertion order (stable for the
// first time each distinct value appears). Shared by the reconciler when
// merging Task.spec.permissions.* with ClusterAgentClass.spec.requires.*
// so the effective allowlist is a single source of truth.
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
