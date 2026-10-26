/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"path"
	"slices"

	corev1 "k8s.io/api/core/v1"
)

// SecretMountRoot is the directory where the controller mounts projected
// Secrets inside the agent pod. Agents read files from
// /run/secrets/<secret-name>/<key>. The controller does not support env-var
// injection by design: env leaks to /proc and to child processes.
const SecretMountRoot = "/run/secrets"

// BuildSecretVolumes turns the declared secret list of the Task into one
// Volume per Secret, plus one VolumeMount that points each Volume at
// /run/secrets/<name>. It returns (nil, nil) when the Task declares no
// secrets, so callers can emit a pod without the scaffolding in that case.
//
// The function mounts each Secret read-only. This is redundant with
// readOnlyRootFilesystem, but explicit. The runAsUser of the pod owns the
// files. The volumes are not optional: a missing Secret keeps the pod in
// ContainerCreating with a FailedMount event. By design, the user diagnoses
// that with `kubectl describe pod`, as in stock Kubernetes.
func BuildSecretVolumes(secrets []string) ([]corev1.Volume, []corev1.VolumeMount) {
	if len(secrets) == 0 {
		return nil, nil
	}

	names := slices.Clone(secrets)
	slices.Sort(names)

	volumes := make([]corev1.Volume, 0, len(names))
	mounts := make([]corev1.VolumeMount, 0, len(names))

	for _, name := range names {
		volName := secretVolumeName(name)
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: name,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			ReadOnly:  true,
			MountPath: path.Join(SecretMountRoot, name),
		})
	}
	return volumes, mounts
}

// secretVolumeName derives a DNS-1123 volume name from a Secret name.
// Secret names already obey DNS-1123, so the function only adds a prefix
// to prevent a collision with other volumes, such as scratch.
func secretVolumeName(secretName string) string {
	return "secret-" + secretName
}
