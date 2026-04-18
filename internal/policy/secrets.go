/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"path"
	"sort"

	corev1 "k8s.io/api/core/v1"
)

// SecretMountRoot is where projected Secrets land inside the agent pod.
// Agents read files from /run/secrets/<secret-name>/<key>. Env-var injection
// is deliberately not supported — env leaks to /proc and to child processes.
const SecretMountRoot = "/run/secrets"

// BuildSecretVolumes turns the Task's declared secret list into one Volume
// per Secret plus a VolumeMount pointing each into /run/secrets/<name>.
// Returns (nil, nil) when no secrets are declared, so callers can emit a
// pod without the scaffolding in the zero-secret case.
//
// Each Secret is mounted read-only (redundant with readOnlyRootFilesystem,
// but explicit) and ownership is the pod's runAsUser. Missing Secrets do
// not prevent the pod from starting — we intentionally leave that to the
// user to diagnose via `kubectl describe pod`, mirroring stock K8s behavior.
func BuildSecretVolumes(secrets []string) ([]corev1.Volume, []corev1.VolumeMount) {
	if len(secrets) == 0 {
		return nil, nil
	}

	names := append([]string(nil), secrets...)
	sort.Strings(names)

	volumes := make([]corev1.Volume, 0, len(names))
	mounts := make([]corev1.VolumeMount, 0, len(names))
	readOnly := true

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
			ReadOnly:  readOnly,
			MountPath: path.Join(SecretMountRoot, name),
		})
	}
	return volumes, mounts
}

// secretVolumeName derives a DNS-1123-friendly volume name from a Secret
// name. Secret names already obey DNS-1123, so this just prefixes them
// to avoid colliding with other volumes (scratch, etc.).
func secretVolumeName(secretName string) string {
	return "secret-" + secretName
}
