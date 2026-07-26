/*
Copyright 2026.
*/

package policy

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func rawConfig(s string) *runtime.RawExtension {
	return &runtime.RawExtension{Raw: []byte(s)}
}

func TestBuildConfigProjection_NilConfig(t *testing.T) {
	envs, vol, mount, err := BuildConfigProjection("task-1", nil, conveyorv1alpha1.AgentInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if envs != nil || vol != nil || mount != nil {
		t.Errorf("expected all nil for nil config, got %+v / %+v / %+v", envs, vol, mount)
	}
}

func TestBuildConfigProjection_ScalarsBecomeSortedEnv(t *testing.T) {
	cfg := rawConfig(`{"TARGET_REPO":"https://github.com/o/r.git","COUNT":3,"DRY_RUN":true}`)
	envs, vol, mount, err := BuildConfigProjection("task-1", cfg, conveyorv1alpha1.AgentInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vol != nil || mount != nil {
		t.Errorf("expected no volume without a declared mount, got %+v / %+v", vol, mount)
	}
	want := []struct{ name, value string }{
		{"COUNT", "3"},
		{"DRY_RUN", "true"},
		{"TARGET_REPO", "https://github.com/o/r.git"},
	}
	if len(envs) != len(want) {
		t.Fatalf("expected %d env vars, got %d: %+v", len(want), len(envs), envs)
	}
	for i, w := range want {
		if envs[i].Name != w.name || envs[i].Value != w.value {
			t.Errorf("env[%d]: expected %s=%s, got %s=%s", i, w.name, w.value, envs[i].Name, envs[i].Value)
		}
	}
}

func TestBuildConfigProjection_MalformedJSONIsPermanent(t *testing.T) {
	_, _, _, err := BuildConfigProjection("task-1", rawConfig(`["not","an","object"]`), conveyorv1alpha1.AgentInputs{})
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("expected PermanentError for non-object config, got %v", err)
	}
}

func TestBuildConfigProjection_UnprojectableFieldWithoutMountIsPermanent(t *testing.T) {
	cfg := rawConfig(`{"nested":{"a":1}}`)
	_, _, _, err := BuildConfigProjection("task-1", cfg, conveyorv1alpha1.AgentInputs{})
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("expected PermanentError for nested field with no config mount/env, got %v", err)
	}
}

func TestBuildConfigProjection_UnprojectableFieldSkippedWithMount(t *testing.T) {
	cfg := rawConfig(`{"nested":{"a":1},"PLAIN":"x"}`)
	inputs := conveyorv1alpha1.AgentInputs{
		Config: &conveyorv1alpha1.AgentInputMount{Mount: "/task/config.json"},
	}
	envs, vol, mount, err := BuildConfigProjection("task-1", cfg, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(envs) != 1 || envs[0].Name != "PLAIN" {
		t.Errorf("expected only PLAIN env var, got %+v", envs)
	}
	if vol == nil || mount == nil {
		t.Fatal("expected volume + mount for declared config mount")
	}
	if vol.ConfigMap == nil || vol.ConfigMap.Name != ConfigConfigMapName("task-1") {
		t.Errorf("volume should reference the owned config ConfigMap, got %+v", vol.VolumeSource)
	}
	if mount.MountPath != "/task/config.json" || mount.SubPath != ConfigKey || !mount.ReadOnly {
		t.Errorf("unexpected mount: %+v", mount)
	}
}

func TestBuildConfigProjection_WholeJSONEnv(t *testing.T) {
	cfg := rawConfig(`{"TARGET_REPO":"o/r"}`)
	inputs := conveyorv1alpha1.AgentInputs{
		Config: &conveyorv1alpha1.AgentInputMount{Env: "AGENT_CONFIG"},
	}
	envs, _, _, err := BuildConfigProjection("task-1", cfg, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("expected scalar env + whole-JSON env, got %+v", envs)
	}
	last := envs[len(envs)-1]
	if last.Name != "AGENT_CONFIG" || last.Value != `{"TARGET_REPO":"o/r"}` {
		t.Errorf("expected AGENT_CONFIG to carry the raw JSON, got %s=%s", last.Name, last.Value)
	}
}

func TestBuildConfigConfigMap(t *testing.T) {
	cfg := rawConfig(`{"A":"b"}`)
	mountInputs := conveyorv1alpha1.AgentInputs{
		Config: &conveyorv1alpha1.AgentInputMount{Mount: "/task/config.json"},
	}

	if cm := BuildConfigConfigMap("task-1", "ns", nil, mountInputs); cm != nil {
		t.Errorf("expected nil ConfigMap for nil config, got %+v", cm)
	}
	if cm := BuildConfigConfigMap("task-1", "ns", cfg, conveyorv1alpha1.AgentInputs{}); cm != nil {
		t.Errorf("expected nil ConfigMap without a declared mount, got %+v", cm)
	}

	cm := BuildConfigConfigMap("task-1", "ns", cfg, mountInputs)
	if cm == nil {
		t.Fatal("expected a ConfigMap")
	}
	if cm.Name != ConfigConfigMapName("task-1") || cm.Namespace != "ns" {
		t.Errorf("unexpected ObjectMeta: %+v", cm.ObjectMeta)
	}
	if cm.Data[ConfigKey] != `{"A":"b"}` {
		t.Errorf("unexpected data: %+v", cm.Data)
	}
}
