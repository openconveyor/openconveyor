/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func TestApplyFilters(t *testing.T) {
	body := []byte(`{"action":"opened","issue":{"number":42}}`)

	cases := []struct {
		name    string
		filters []conveyorv1alpha1.WebhookFilter
		want    bool
	}{
		{name: "no filters match everything", want: true},
		{
			name: "single match",
			filters: []conveyorv1alpha1.WebhookFilter{
				{Path: "action", Equals: "opened"},
			},
			want: true,
		},
		{
			name: "AND of matching filters",
			filters: []conveyorv1alpha1.WebhookFilter{
				{Path: "action", Equals: "opened"},
				{Path: "issue.number", Equals: "42"},
			},
			want: true,
		},
		{
			name: "one mismatched drops",
			filters: []conveyorv1alpha1.WebhookFilter{
				{Path: "action", Equals: "opened"},
				{Path: "issue.number", Equals: "99"},
			},
			want: false,
		},
		{
			name: "missing path drops",
			filters: []conveyorv1alpha1.WebhookFilter{
				{Path: "pull_request.merged", Equals: "true"},
			},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplyFilters(body, tc.filters)
			if got != tc.want {
				t.Fatalf("ApplyFilters = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildTask(t *testing.T) {
	tmpl := conveyorv1alpha1.TaskTemplate{
		Namespace:          "conveyor-tasks",
		GenerateNamePrefix: "gh-issue-",
		Agent:              conveyorv1alpha1.AgentRef{Ref: "claude-code-implementer"},
		Permissions: conveyorv1alpha1.Permissions{
			Secrets: []string{"github-token"},
		},
		Resources: conveyorv1alpha1.TaskResources{
			Timeout: metav1.Duration{Duration: 30 * time.Minute},
		},
		Mappings: []conveyorv1alpha1.FieldMapping{
			{From: "issue.title", To: "prompt"},
			{From: "issue.number", To: "labels.issue-number"},
			{From: "sender.login", To: "annotations.requester", Default: "unknown"},
		},
	}

	body := []byte(`{"issue":{"title":"fix bug","number":42},"sender":{"login":"octocat"}}`)

	task, err := BuildTask(tmpl, body)
	if err != nil {
		t.Fatalf("BuildTask: %v", err)
	}

	if task.GenerateName != "gh-issue-" {
		t.Errorf("GenerateName = %q, want gh-issue-", task.GenerateName)
	}
	if task.Namespace != "conveyor-tasks" {
		t.Errorf("Namespace = %q, want conveyor-tasks", task.Namespace)
	}
	if task.Spec.Prompt.Inline != "fix bug" {
		t.Errorf("prompt.inline = %q, want \"fix bug\"", task.Spec.Prompt.Inline)
	}
	if task.Labels["issue-number"] != "42" {
		t.Errorf("labels[issue-number] = %q, want 42", task.Labels["issue-number"])
	}
	if task.Annotations["requester"] != "octocat" {
		t.Errorf("annotations[requester] = %q, want octocat", task.Annotations["requester"])
	}
	if got := task.Spec.Permissions.Secrets; len(got) != 1 || got[0] != "github-token" {
		t.Errorf("permissions.secrets = %v, want [github-token]", got)
	}
}

func TestBuildTask_NameOverride(t *testing.T) {
	tmpl := conveyorv1alpha1.TaskTemplate{
		GenerateNamePrefix: "ignored-",
		Agent:              conveyorv1alpha1.AgentRef{Ref: "x"},
		Resources:          conveyorv1alpha1.TaskResources{Timeout: metav1.Duration{Duration: time.Minute}},
		Mappings: []conveyorv1alpha1.FieldMapping{
			{From: "repository.full_name", To: "name"},
			{From: "issue.title", To: "prompt"},
		},
	}
	body := []byte(`{"repository":{"full_name":"acme-ci"},"issue":{"title":"do work"}}`)

	task, err := BuildTask(tmpl, body)
	if err != nil {
		t.Fatalf("BuildTask: %v", err)
	}
	if task.Name != "acme-ci" {
		t.Errorf("Name = %q, want acme-ci", task.Name)
	}
	if task.GenerateName != "" {
		t.Errorf("GenerateName = %q, want empty when name is set", task.GenerateName)
	}
}

func TestBuildTask_DefaultApplied(t *testing.T) {
	tmpl := conveyorv1alpha1.TaskTemplate{
		Agent:     conveyorv1alpha1.AgentRef{Ref: "x"},
		Resources: conveyorv1alpha1.TaskResources{Timeout: metav1.Duration{Duration: time.Minute}},
		Mappings: []conveyorv1alpha1.FieldMapping{
			{From: "missing.path", To: "prompt", Default: "fallback prompt"},
		},
	}
	task, err := BuildTask(tmpl, []byte(`{}`))
	if err != nil {
		t.Fatalf("BuildTask: %v", err)
	}
	if task.Spec.Prompt.Inline != "fallback prompt" {
		t.Errorf("prompt.inline = %q, want fallback prompt", task.Spec.Prompt.Inline)
	}
}

func TestBuildTask_NoPromptIsError(t *testing.T) {
	tmpl := conveyorv1alpha1.TaskTemplate{
		Agent:     conveyorv1alpha1.AgentRef{Ref: "x"},
		Resources: conveyorv1alpha1.TaskResources{Timeout: metav1.Duration{Duration: time.Minute}},
	}
	_, err := BuildTask(tmpl, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for missing prompt, got nil")
	}
}

func TestBuildTask_UnknownMapping(t *testing.T) {
	tmpl := conveyorv1alpha1.TaskTemplate{
		Agent:     conveyorv1alpha1.AgentRef{Ref: "x"},
		Resources: conveyorv1alpha1.TaskResources{Timeout: metav1.Duration{Duration: time.Minute}},
		Mappings: []conveyorv1alpha1.FieldMapping{
			{From: "foo", To: "bogus.field", Default: "v"},
		},
	}
	_, err := BuildTask(tmpl, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown mapping target")
	}
}
