/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// PromptKey is the key used inside the owned ConfigMap when the Task's
// prompt is inline. Kept as a named constant so the controller and any
// future consumers agree on it.
const PromptKey = "prompt"

// PromptVolumeName is the pod-level volume name that carries the prompt.
const PromptVolumeName = "prompt"

// InlinePromptConfigMapName returns the name of the owned ConfigMap that
// holds an inline prompt. Separate from the Task's main resource name so
// it does not collide with the SA / Role / Job sharing `<task-name>`.
func InlinePromptConfigMapName(taskName string) string {
	return taskName + "-prompt"
}

// BuildPromptProjection returns the volume and volume-mount that project
// the Task's prompt into the agent container at the AgentClass-declared
// mount path. Returns (nil, nil, nil) when the AgentClass does not
// declare a prompt input — some agents (e.g. a cron reporter with its
// prompt baked into the image) do not read a Task-supplied prompt.
//
// The mount uses subPath so the target path is a single file rather
// than a directory, letting the agent `cat $MOUNT` directly.
//
// Prompt sources map as follows:
//
//   - inline            → project from the controller-owned ConfigMap
//   - secretRef         → project from the referenced Secret (key preserved)
//   - configMapRef      → project from the referenced ConfigMap (key preserved)
//
// Inline prompts rely on the caller having already created the owned
// ConfigMap; this function only builds the projection metadata.
func BuildPromptProjection(
	taskName string,
	prompt conveyorv1alpha1.PromptSource,
	inputs conveyorv1alpha1.AgentInputs,
) (*corev1.Volume, *corev1.VolumeMount, error) {
	if inputs.Prompt == nil || inputs.Prompt.Mount == "" {
		// Env-var delivery is not supported yet; documented in the type.
		return nil, nil, nil
	}

	mountPath := inputs.Prompt.Mount
	vol := &corev1.Volume{Name: PromptVolumeName}
	readOnly := true

	var subPath string
	switch {
	case prompt.Inline != "":
		subPath = PromptKey
		vol.VolumeSource = corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: InlinePromptConfigMapName(taskName),
				},
			},
		}

	case prompt.ConfigMapRef != nil:
		subPath = prompt.ConfigMapRef.Key
		vol.VolumeSource = corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: prompt.ConfigMapRef.Name,
				},
			},
		}

	case prompt.SecretRef != nil:
		subPath = prompt.SecretRef.Key
		vol.VolumeSource = corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: prompt.SecretRef.Name,
			},
		}

	default:
		return nil, nil, fmt.Errorf("prompt has no source (inline/secretRef/configMapRef)")
	}

	mount := &corev1.VolumeMount{
		Name:      PromptVolumeName,
		MountPath: mountPath,
		SubPath:   subPath,
		ReadOnly:  readOnly,
	}
	return vol, mount, nil
}

// BuildInlinePromptConfigMap materialises the owned ConfigMap that holds
// an inline prompt. Returns nil when the prompt is not inline — the
// reconciler should then make sure no such ConfigMap lingers from a
// prior state.
func BuildInlinePromptConfigMap(taskName, namespace string, prompt conveyorv1alpha1.PromptSource) *corev1.ConfigMap {
	if prompt.Inline == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      InlinePromptConfigMapName(taskName),
			Namespace: namespace,
			Labels:    OwnershipLabels(taskName),
		},
		Data: map[string]string{
			PromptKey: prompt.Inline,
		},
	}
}
