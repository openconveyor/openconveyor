/*
Copyright 2026.
*/

package policy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestBuildSecretVolumes(t *testing.T) {
	tests := []struct {
		name     string
		secrets  []string
		validate func(t *testing.T, vols []corev1.Volume, mounts []corev1.VolumeMount)
	}{
		{
			name:    "empty returns nil",
			secrets: nil,
			validate: func(t *testing.T, vols []corev1.Volume, mounts []corev1.VolumeMount) {
				if vols != nil || mounts != nil {
					t.Errorf("expected nil,nil; got %v, %v", vols, mounts)
				}
			},
		},
		{
			name:    "projects each secret as read-only file mount",
			secrets: []string{"alpha", "bravo"},
			validate: func(t *testing.T, vols []corev1.Volume, mounts []corev1.VolumeMount) {
				if len(vols) != 2 || len(mounts) != 2 {
					t.Fatalf("expected 2 volumes + 2 mounts; got %d, %d", len(vols), len(mounts))
				}
				if vols[0].Secret == nil || vols[0].Secret.SecretName != "alpha" {
					t.Errorf("volume 0 secret name: got %+v", vols[0].Secret)
				}
				if mounts[0].MountPath != "/run/secrets/alpha" {
					t.Errorf("mount path 0: got %q", mounts[0].MountPath)
				}
				if !mounts[0].ReadOnly {
					t.Error("mount should be read-only")
				}
				if mounts[0].Name != vols[0].Name {
					t.Errorf("volume/mount name mismatch: %q vs %q", vols[0].Name, mounts[0].Name)
				}
			},
		},
		{
			name:    "sorted for determinism",
			secrets: []string{"zulu", "alpha", "mike"},
			validate: func(t *testing.T, vols []corev1.Volume, _ []corev1.VolumeMount) {
				if vols[0].Secret.SecretName != "alpha" ||
					vols[1].Secret.SecretName != "mike" ||
					vols[2].Secret.SecretName != "zulu" {
					t.Errorf("expected sorted order, got %q, %q, %q",
						vols[0].Secret.SecretName, vols[1].Secret.SecretName, vols[2].Secret.SecretName)
				}
			},
		},
		{
			name:    "volume name is prefixed",
			secrets: []string{"my-secret"},
			validate: func(t *testing.T, vols []corev1.Volume, _ []corev1.VolumeMount) {
				if vols[0].Name != "secret-my-secret" {
					t.Errorf("volume name: got %q want %q", vols[0].Name, "secret-my-secret")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vols, mounts := BuildSecretVolumes(tc.secrets)
			tc.validate(t, vols, mounts)
		})
	}
}
