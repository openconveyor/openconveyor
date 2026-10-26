/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// The package applies these labels to every resource that it generates,
// so the reconciler and kubectl can find the resources by selector. This
// file defines the labels so the controller and the policy package use
// the same label schema.
const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	LabelComponent = "app.kubernetes.io/component"
	LabelTaskName  = "openconveyor.ai/task"
	ManagedByValue = "conveyor"
	ComponentTask  = "task"
)

// PodSelectorLabels returns the labels that the controller sets on
// NetworkPolicy.podSelector and on the pod template, so the policy binds
// to the pod.
func PodSelectorLabels(taskName string) map[string]string {
	return map[string]string{
		LabelTaskName: taskName,
	}
}

// OwnershipLabels returns the labels that the package applies to every
// generated resource.
func OwnershipLabels(taskName string) map[string]string {
	return map[string]string{
		LabelManagedBy: ManagedByValue,
		LabelTaskName:  taskName,
	}
}

// ownedMeta is the shared ObjectMeta for every resource that this package
// generates for a Task.
func ownedMeta(name, namespace, taskName string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    OwnershipLabels(taskName),
	}
}

// MergeStringSets unions the input slices, drops empty entries, and
// returns a deduplicated result in insertion order. The order follows the
// first occurrence of each distinct value. The function uses each entry
// verbatim and does not trim whitespace; resolveEgress trims egress
// entries separately. The reconciler uses the function to merge
// Task.spec.permissions.* with ClusterAgentClass.spec.requires.*, so the
// effective allowlist has a single source of truth.
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
