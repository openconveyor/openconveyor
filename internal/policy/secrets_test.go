/*
Copyright 2026.
*/

package policy

import (
	"testing"
)

func TestBuildSecretVolumes_EmptyReturnsNil(t *testing.T) {
	vols, mounts := BuildSecretVolumes(nil)
	if vols != nil || mounts != nil {
		t.Errorf("expected nil,nil; got %v, %v", vols, mounts)
	}
}

func TestBuildSecretVolumes_ProjectsEachSecret(t *testing.T) {
	vols, mounts := BuildSecretVolumes([]string{"alpha", "bravo"})
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
}

func TestBuildSecretVolumes_SortedForDeterminism(t *testing.T) {
	vols, _ := BuildSecretVolumes([]string{"zulu", "alpha", "mike"})
	if vols[0].Secret.SecretName != "alpha" ||
		vols[1].Secret.SecretName != "mike" ||
		vols[2].Secret.SecretName != "zulu" {
		t.Errorf("expected sorted order, got %q, %q, %q",
			vols[0].Secret.SecretName, vols[1].Secret.SecretName, vols[2].Secret.SecretName)
	}
}

func TestBuildSecretVolumes_NamePrefixed(t *testing.T) {
	vols, _ := BuildSecretVolumes([]string{"my-secret"})
	if vols[0].Name != "secret-my-secret" {
		t.Errorf("volume name: got %q want %q", vols[0].Name, "secret-my-secret")
	}
}
