/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"k8s.io/apimachinery/pkg/runtime"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// defaultGenerateNamePrefix mirrors the CRD default for
// spec.task.generateNamePrefix (clustertriggerclass_types.go), so Tasks
// from templates that predate the default still have a prefix.
const defaultGenerateNamePrefix = "task-"

// ApplyFilters returns true when every filter matches the body. The
// function ANDs the filters; a zero-length slice matches everything. The
// match compares the String() form of the gjson result to filter.Equals.
// This is adequate for the "start only on a specific action or state" use
// case, and it does not commit to a full predicate language.
func ApplyFilters(body []byte, filters []conveyorv1alpha1.WebhookFilter) bool {
	for _, f := range filters {
		got := gjson.GetBytes(body, f.Path)
		if got.String() != f.Equals {
			return false
		}
	}
	return true
}

// BuildTask creates a Task from the template and the webhook payload.
//
// A "name" field mapping sets the name as an absolute override. Without
// that mapping, the function sets generateName=<prefix>, so the apiserver
// generates the name. A mapping that resolves to an empty string falls
// back to FieldMapping.Default. The function drops a mapping that does not
// resolve and has no default.
func BuildTask(tmpl conveyorv1alpha1.TaskTemplate, body []byte) (*conveyorv1alpha1.Task, error) {
	task := &conveyorv1alpha1.Task{
		Spec: conveyorv1alpha1.TaskSpec{
			// DeepCopy: Agent.Config is a pointer, and config mappings
			// mutate it. The shared template must stay untouched.
			Agent:       *tmpl.Agent.DeepCopy(),
			Permissions: *tmpl.Permissions.DeepCopy(),
			Resources:   tmpl.Resources,
		},
	}
	if tmpl.TTLSecondsAfterFinished != nil {
		ttl := *tmpl.TTLSecondsAfterFinished
		task.Spec.TTLSecondsAfterFinished = &ttl
	}

	prefix := tmpl.GenerateNamePrefix
	if prefix == "" {
		prefix = defaultGenerateNamePrefix
	}
	task.GenerateName = prefix

	if tmpl.Namespace != "" {
		task.Namespace = tmpl.Namespace
	}

	for _, m := range tmpl.Mappings {
		val := resolveMapping(body, m)
		if val == "" {
			continue
		}
		if err := applyMapping(task, m.To, val); err != nil {
			return nil, fmt.Errorf("mapping %q → %q: %w", m.From, m.To, err)
		}
	}

	// A Task must have a prompt source before the reconciler sees it. The
	// function reports that here, instead of letting the CRD validation
	// reject the Task after the 200 response.
	p := task.Spec.Prompt
	if p.Inline == "" && p.SecretRef == nil && p.ConfigMapRef == nil {
		return nil, errors.New(`webhook produced a Task with no prompt — add a "prompt" mapping or a default`)
	}

	return task, nil
}

func resolveMapping(body []byte, m conveyorv1alpha1.FieldMapping) string {
	got := gjson.GetBytes(body, m.From)
	if !got.Exists() {
		return m.Default
	}
	if got.String() == "" {
		return m.Default
	}
	return got.String()
}

// applyMapping honors the documented `to` vocabulary:
//
//	prompt              → Task.spec.prompt.inline
//	name                → metadata.name (clears generateName)
//	labels.<key>        → metadata.labels[key]
//	annotations.<key>   → metadata.annotations[key]
//	config.<key>        → Task.spec.agent.config[key] (projected as $<key>)
func applyMapping(task *conveyorv1alpha1.Task, to, value string) error {
	switch {
	case to == "prompt":
		task.Spec.Prompt.Inline = value
	case to == "name":
		task.Name = value
		task.GenerateName = ""
	case strings.HasPrefix(to, "labels."):
		setMapKey(&task.Labels, strings.TrimPrefix(to, "labels."), value)
	case strings.HasPrefix(to, "annotations."):
		setMapKey(&task.Annotations, strings.TrimPrefix(to, "annotations."), value)
	case strings.HasPrefix(to, "config."):
		return setConfigKey(&task.Spec.Agent, strings.TrimPrefix(to, "config."), value)
	default:
		return fmt.Errorf("unknown mapping target %q", to)
	}
	return nil
}

// setConfigKey merges a single string value into spec.agent.config and
// keeps any config that is already on the template. A mapped value
// overrides a template value on a key collision.
func setConfigKey(agent *conveyorv1alpha1.AgentRef, key, value string) error {
	if key == "" {
		return errors.New(`config mapping needs a key, e.g. "config.TARGET_REPO"`)
	}
	fields := map[string]any{}
	if agent.Config != nil && len(agent.Config.Raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(agent.Config.Raw))
		dec.UseNumber() // keep template numbers as written across the re-marshal
		if err := dec.Decode(&fields); err != nil {
			return fmt.Errorf("template agent.config is not a JSON object: %w", err)
		}
	}
	fields[key] = value
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	agent.Config = &runtime.RawExtension{Raw: raw}
	return nil
}

func setMapKey(m *map[string]string, key, value string) {
	if *m == nil {
		*m = map[string]string{}
	}
	(*m)[key] = value
}
