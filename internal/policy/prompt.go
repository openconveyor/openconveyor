/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"errors"

	corev1 "k8s.io/api/core/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// PromptProjection is the volume and mount pair that delivers the prompt
// of the Task into the container. The zero value means the AgentClass
// does not read a prompt.
type PromptProjection struct {
	Volume *corev1.Volume
	Mount  *corev1.VolumeMount
}

// PromptKey is the key inside the owned ConfigMap when the prompt of the
// Task is inline. It is a named constant so the controller and future
// consumers agree on it.
const PromptKey = "prompt"

// PromptVolumeName is the pod-level volume name that carries the prompt.
const PromptVolumeName = "prompt"

// promptSuffix distinguishes the prompt ConfigMap from the Task's main
// resource name and the agent-config ConfigMap.
const promptSuffix = "-prompt"

// InlinePromptConfigMapName returns the name of the owned ConfigMap that
// holds an inline prompt. The name differs from the main resource name of
// the Task, so it does not collide with the SA, Role and Job that share
// `<task-name>`.
func InlinePromptConfigMapName(taskName string) string {
	return taskName + promptSuffix
}

// BuildPromptProjection returns the volume and the volume mount that
// project the prompt of the Task into the agent container. The mount path
// is the one that the AgentClass declares. The function returns an empty
// PromptProjection and no error when the AgentClass does not declare a
// prompt input. Some agents, for example a cron reporter with its prompt
// built into the image, do not read a prompt from the Task.
//
// The mount uses subPath, so the target path is a single file and not a
// directory. The agent can `cat $MOUNT` directly.
//
// Prompt sources map as follows:
//
//   - inline            → project from the controller-owned ConfigMap
//   - secretRef         → project from the referenced Secret (key preserved)
//   - configMapRef      → project from the referenced ConfigMap (key preserved)
//
// An inline prompt requires that the caller has already created the owned
// ConfigMap. This function only builds the projection metadata.
func BuildPromptProjection(
	taskName string,
	prompt conveyorv1alpha1.PromptSource,
	inputs conveyorv1alpha1.AgentInputs,
) (PromptProjection, error) {
	if inputs.Prompt == nil || inputs.Prompt.Mount == "" {
		// The controller does not support env-var delivery yet. The type documents this.
		return PromptProjection{}, nil
	}

	mountPath := inputs.Prompt.Mount
	vol := &corev1.Volume{Name: PromptVolumeName}

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
		return PromptProjection{}, errors.New("prompt has no source (inline/secretRef/configMapRef)")
	}

	return PromptProjection{
		Volume: vol,
		Mount: &corev1.VolumeMount{
			Name:      PromptVolumeName,
			MountPath: mountPath,
			SubPath:   subPath,
			ReadOnly:  true,
		},
	}, nil
}

// BuildInlinePromptConfigMap creates the owned ConfigMap that holds an
// inline prompt. It returns nil when the prompt is not inline. The
// reconciler must then make sure that no such ConfigMap remains from a
// previous state.
func BuildInlinePromptConfigMap(taskName, namespace string, prompt conveyorv1alpha1.PromptSource) *corev1.ConfigMap {
	if prompt.Inline == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: ownedMeta(InlinePromptConfigMapName(taskName), namespace, taskName),
		Data: map[string]string{
			PromptKey: prompt.Inline,
		},
	}
}
