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
	proj, err := BuildConfigProjection("task-1", nil, conveyorv1alpha1.AgentInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proj.Env != nil || proj.Volume != nil || proj.Mount != nil {
		t.Errorf("expected zero projection for nil config, got %+v", proj)
	}
}

func TestBuildConfigProjection_ScalarsBecomeSortedEnv(t *testing.T) {
	cfg := rawConfig(`{"TARGET_REPO":"https://github.com/o/r.git","COUNT":3,"DRY_RUN":true}`)
	proj, err := BuildConfigProjection("task-1", cfg, conveyorv1alpha1.AgentInputs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if proj.Volume != nil || proj.Mount != nil {
		t.Errorf("expected no volume without a declared mount, got %+v / %+v", proj.Volume, proj.Mount)
	}
	envs := proj.Env
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
	_, err := BuildConfigProjection("task-1", rawConfig(`["not","an","object"]`), conveyorv1alpha1.AgentInputs{})
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("expected PermanentError for non-object config, got %v", err)
	}
}

func TestBuildConfigProjection_UnprojectableFieldWithoutMountIsPermanent(t *testing.T) {
	cfg := rawConfig(`{"nested":{"a":1}}`)
	_, err := BuildConfigProjection("task-1", cfg, conveyorv1alpha1.AgentInputs{})
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
	proj, err := BuildConfigProjection("task-1", cfg, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proj.Env) != 1 || proj.Env[0].Name != "PLAIN" {
		t.Errorf("expected only PLAIN env var, got %+v", proj.Env)
	}
	if proj.Volume == nil || proj.Mount == nil {
		t.Fatal("expected volume + mount for declared config mount")
	}
	if proj.Volume.ConfigMap == nil || proj.Volume.ConfigMap.Name != ConfigConfigMapName("task-1") {
		t.Errorf("volume should reference the owned config ConfigMap, got %+v", proj.Volume.VolumeSource)
	}
	if proj.Mount.MountPath != "/task/config.json" || proj.Mount.SubPath != ConfigKey || !proj.Mount.ReadOnly {
		t.Errorf("unexpected mount: %+v", proj.Mount)
	}
}

func TestBuildConfigProjection_WholeJSONEnv(t *testing.T) {
	cfg := rawConfig(`{"TARGET_REPO":"o/r"}`)
	inputs := conveyorv1alpha1.AgentInputs{
		Config: &conveyorv1alpha1.AgentInputMount{Env: "AGENT_CONFIG"},
	}
	proj, err := BuildConfigProjection("task-1", cfg, inputs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	envs := proj.Env
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
