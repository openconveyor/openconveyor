/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// ApplyFilters returns true when every filter matches the body. Filters
// are ANDed; a zero-length slice matches everything. Matching compares
// the gjson result's String() form to filter.Equals — adequate for the
// "fire only on a specific action/state" use case without committing to
// a full predicate language.
func ApplyFilters(body []byte, filters []conveyorv1alpha1.WebhookFilter) bool {
	for _, f := range filters {
		got := gjson.GetBytes(body, f.Path)
		if got.String() != f.Equals {
			return false
		}
	}
	return true
}

// BuildTask materialises a Task from the template + webhook payload.
//
// The name is either set by a "name" field mapping (absolute override)
// or left as generateName=<prefix> so the apiserver generates one.
// Mappings that resolve to an empty string fall back to FieldMapping.Default;
// mappings that don't resolve at all and have no default are dropped.
func BuildTask(tmpl conveyorv1alpha1.TaskTemplate, body []byte) (*conveyorv1alpha1.Task, error) {
	task := &conveyorv1alpha1.Task{
		Spec: conveyorv1alpha1.TaskSpec{
			Agent:       tmpl.Agent,
			Permissions: *tmpl.Permissions.DeepCopy(),
			Resources:   tmpl.Resources,
		},
	}

	prefix := tmpl.GenerateNamePrefix
	if prefix == "" {
		prefix = "task-"
	}
	task.ObjectMeta.GenerateName = prefix

	if tmpl.Namespace != "" {
		task.ObjectMeta.Namespace = tmpl.Namespace
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

	// A Task must have at least some prompt source by the time the
	// reconciler sees it; surface that here instead of letting the CRD
	// validation fire after the 200 response.
	p := task.Spec.Prompt
	if p.Inline == "" && p.SecretRef == nil && p.ConfigMapRef == nil {
		return nil, fmt.Errorf("webhook produced a Task with no prompt — add a \"prompt\" mapping or a default")
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

// applyMapping honours the documented `to` vocabulary:
//
//	prompt              → Task.spec.prompt.inline
//	name                → metadata.name (clears generateName)
//	labels.<key>        → metadata.labels[key]
//	annotations.<key>   → metadata.annotations[key]
func applyMapping(task *conveyorv1alpha1.Task, to, value string) error {
	switch {
	case to == "prompt":
		task.Spec.Prompt.Inline = value
	case to == "name":
		task.ObjectMeta.Name = value
		task.ObjectMeta.GenerateName = ""
	case strings.HasPrefix(to, "labels."):
		setMetaString(&task.ObjectMeta, "labels", strings.TrimPrefix(to, "labels."), value)
	case strings.HasPrefix(to, "annotations."):
		setMetaString(&task.ObjectMeta, "annotations", strings.TrimPrefix(to, "annotations."), value)
	default:
		return fmt.Errorf("unknown mapping target %q", to)
	}
	return nil
}

func setMetaString(meta *metav1.ObjectMeta, kind, key, value string) {
	switch kind {
	case "labels":
		if meta.Labels == nil {
			meta.Labels = map[string]string{}
		}
		meta.Labels[key] = value
	case "annotations":
		if meta.Annotations == nil {
			meta.Annotations = map[string]string{}
		}
		meta.Annotations[key] = value
	}
}
