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
	"maps"
	"regexp"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// ConfigKey is the key inside the owned ConfigMap when the agent declares
// a config mount. The subPath mount also uses it as the projected file name.
const ConfigKey = "config.json"

// ConfigVolumeName is the pod-level volume name that carries the config.
const ConfigVolumeName = "config"

// envNameRe matches valid POSIX environment variable names.
var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ConfigProjection is how spec.agent.config reaches the container. The
// scalar fields become env vars. When the AgentClass declares a config
// mount, a volume and a mount carry the full JSON. The zero value means
// the Task has no config.
type ConfigProjection struct {
	Env    []corev1.EnvVar
	Volume *corev1.Volume
	Mount  *corev1.VolumeMount
}

// configSuffix distinguishes the agent-config ConfigMap from the Task's
// main resource name and the prompt ConfigMap.
const configSuffix = "-config"

// AgentConfigMapName returns the name of the owned ConfigMap that holds
// the agent config JSON of the Task. The name differs from the main
// resource name of the Task and from the prompt ConfigMap name.
func AgentConfigMapName(taskName string) string {
	return taskName + configSuffix
}

// BuildConfigProjection turns Task.spec.agent.config into container env
// vars. When the AgentClass declares a config input, it also returns a
// volume and a mount that carry the full JSON.
//
// Delivery contract:
//
//   - Top-level scalar fields (string, number, bool) become env vars with
//     the field name. The function sorts them for determinism. The
//     reference agents consume these ($TARGET_REPO, $REPORT_ISSUE_URL, ...).
//   - When the AgentClass sets inputs.config.mount, the controller projects
//     the full JSON as a single file at that path from the controller-owned
//     ConfigMap.
//   - When the AgentClass sets inputs.config.env, the controller delivers
//     the full JSON in that env var.
//
// Some fields cannot become env vars: nested objects, arrays, null values,
// and names that are not valid env identifiers. The agent can reach these
// fields only through the full JSON. If the AgentClass declares neither a
// mount nor an env, such a field is a PermanentError, because a retry
// cannot fix a spec problem.
//
// Config is for non-secret data only. Env vars leak into /proc, so secrets
// must go through permissions.secrets (ADR-0004).
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

	keys := slices.Sorted(maps.Keys(fields))

	var envs []corev1.EnvVar
	for _, k := range keys {
		val, ok := scalarString(fields[k])
		if !ok || !envNameRe.MatchString(k) {
			if hasMount || hasWholeEnv {
				continue // reachable through the full JSON
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
						Name: AgentConfigMapName(taskName),
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

// BuildAgentConfigMap creates the owned ConfigMap that carries the config
// JSON for agents with a declared config mount. It returns nil when there
// is nothing to mount. The reconciler must then make sure that no such
// ConfigMap remains from a previous state.
func BuildAgentConfigMap(
	taskName, namespace string,
	config *runtime.RawExtension,
	inputs conveyorv1alpha1.AgentInputs,
) *corev1.ConfigMap {
	if config == nil || len(config.Raw) == 0 || inputs.Config == nil || inputs.Config.Mount == "" {
		return nil
	}
	return &corev1.ConfigMap{
		ObjectMeta: ownedMeta(AgentConfigMapName(taskName), namespace, taskName),
		Data: map[string]string{
			ConfigKey: string(config.Raw),
		},
	}
}

// decodeConfigObject parses the raw config as a JSON object. It keeps
// number literals as written and does not round-trip them through float64.
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
		return strconv.FormatBool(t), true
	default:
		return "", false
	}
}
