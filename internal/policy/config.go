/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// ConfigKey is the key used inside the owned ConfigMap (and thus the
// projected file name via subPath) when the agent declares a config mount.
const ConfigKey = "config.json"

// ConfigVolumeName is the pod-level volume name that carries the config.
const ConfigVolumeName = "config"

// envNameRe matches valid POSIX environment variable names.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ConfigProjection is how spec.agent.config reaches the container:
// scalar fields as env vars, plus — when the AgentClass declares a
// config mount — a volume/mount pair carrying the full JSON. Zero value
// means the Task has no config.
type ConfigProjection struct {
	Env    []corev1.EnvVar
	Volume *corev1.Volume
	Mount  *corev1.VolumeMount
}

// ConfigConfigMapName returns the name of the owned ConfigMap that holds
// the Task's agent config JSON. Separate from the Task's main resource
// name and from the prompt ConfigMap.
func ConfigConfigMapName(taskName string) string {
	return taskName + "-config"
}

// BuildConfigProjection turns Task.spec.agent.config into container env
// vars and, when the AgentClass declares a config input, a volume/mount
// carrying the full JSON.
//
// Delivery contract:
//
//   - Top-level scalar fields (string/number/bool) become env vars named
//     after the field, sorted for determinism. This is what the reference
//     agents consume ($TARGET_REPO, $REPORT_ISSUE_URL, ...).
//   - inputs.config.mount, when set, projects the full JSON as a single
//     file at that path (from the controller-owned ConfigMap).
//   - inputs.config.env, when set, delivers the full JSON in that env var.
//
// Fields that cannot be env vars (nested objects/arrays, names that are
// not valid env identifiers, null) are only reachable through the full
// JSON; if the AgentClass declares neither mount nor env, such a field is
// a PermanentError — retrying cannot fix a spec problem.
//
// Config is for non-secret data only: env vars leak into /proc, so
// secrets must go through permissions.secrets (ADR-0004).
func BuildConfigProjection(
	taskName string,
	config *runtime.RawExtension,
	inputs conveyorv1alpha1.AgentInputs,
) (ConfigProjection, error) {
	if config == nil || len(config.Raw) == 0 {
		return ConfigProjection{}, nil
	}

	fields, err := decodeConfigObject(config.Raw)
	if err != nil {
		return ConfigProjection{}, &PermanentError{Err: fmt.Errorf("spec.agent.config: %w", err)}
	}

	hasMount := inputs.Config != nil && inputs.Config.Mount != ""
	hasWholeEnv := inputs.Config != nil && inputs.Config.Env != ""

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var envs []corev1.EnvVar
	for _, k := range keys {
		val, ok := scalarString(fields[k])
		if !ok || !envNameRe.MatchString(k) {
			if hasMount || hasWholeEnv {
				continue // reachable via the full JSON
			}
			return ConfigProjection{}, &PermanentError{Err: fmt.Errorf(
				"spec.agent.config.%s cannot be projected as an env var and the AgentClass declares no config mount/env", k)}
		}
		envs = append(envs, corev1.EnvVar{Name: k, Value: val})
	}

	if hasWholeEnv {
		if !envNameRe.MatchString(inputs.Config.Env) {
			return ConfigProjection{}, &PermanentError{Err: fmt.Errorf(
				"agentclass inputs.config.env %q is not a valid env var name", inputs.Config.Env)}
		}
		envs = append(envs, corev1.EnvVar{Name: inputs.Config.Env, Value: string(config.Raw)})
	}

	if !hasMount {
		return ConfigProjection{Env: envs}, nil
	}

	return ConfigProjection{
		Env: envs,
		Volume: &corev1.Volume{
			Name: ConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ConfigConfigMapName(taskName),
					},
				},
			},
		},
		Mount: &corev1.VolumeMount{
			Name:      ConfigVolumeName,
			MountPath: inputs.Config.Mount,
			SubPath:   ConfigKey,
			ReadOnly:  true,
		},
	}, nil
}

// BuildConfigConfigMap materialises the owned ConfigMap that carries the
// config JSON for agents with a declared config mount. Returns nil when
// there is nothing to mount — the reconciler should then make sure no
// such ConfigMap lingers from a prior state.
func BuildConfigConfigMap(
	taskName, namespace string,
	config *runtime.RawExtension,
	inputs conveyorv1alpha1.AgentInputs,
) *corev1.ConfigMap {
	if config == nil || len(config.Raw) == 0 || inputs.Config == nil || inputs.Config.Mount == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: ownedMeta(ConfigConfigMapName(taskName), namespace, taskName),
		Data: map[string]string{
			ConfigKey: string(config.Raw),
		},
	}
}

// decodeConfigObject parses the raw config as a JSON object, preserving
// number literals as written (no float64 round-trip).
func decodeConfigObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var fields map[string]any
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("must be a JSON object: %w", err)
	}
	return fields, nil
}

// scalarString renders a scalar JSON value as its env-var string form.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		return fmt.Sprintf("%t", t), true
	default:
		return "", false
	}
}
