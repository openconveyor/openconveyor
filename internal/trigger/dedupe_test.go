/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/policy"
)

const issueLabel = "openconveyor.ai/issue-number"

// dedupeTriggerClass is the GitHub class with an issue-number mapping and
// dedupe on that label.
func dedupeTriggerClass(name string) *conveyorv1alpha1.ClusterTriggerClass {
	ctc := githubTriggerClass()
	ctc.Name = name
	ctc.Spec.Path = "/" + name
	ctc.Spec.Task.Mappings = append(ctc.Spec.Task.Mappings,
		conveyorv1alpha1.FieldMapping{From: "issue.number", To: "labels." + issueLabel})
	ctc.Spec.Dedupe = &conveyorv1alpha1.TriggerDedupe{UniqueBy: []string{issueLabel}}
	return ctc
}

// existingTask is a Task that an earlier delivery created.
func existingTask(name, class, issue string, phase conveyorv1alpha1.TaskPhase) *conveyorv1alpha1.Task {
	return &conveyorv1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "conveyor-tasks",
			Labels:    map[string]string{policy.LabelTriggerClass: class, issueLabel: issue},
		},
		Status: conveyorv1alpha1.TaskStatus{Phase: phase},
	}
}

// deliver posts a signed issue-opened event for issue 42 to the class path.
func deliver(t *testing.T, h *Handler, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	sig := "sha256=" + hmacHex(webhookSecret().Data["secret"], []byte(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, path, []byte(body), sig))
	return rec
}

const issue42 = `{"action":"opened","issue":{"title":"fix it","number":42}}`

func countTasks(t *testing.T, c client.Client) int {
	t.Helper()
	var list conveyorv1alpha1.TaskList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	return len(list.Items)
}

func TestDedupe(t *testing.T) {
	tests := []struct {
		name      string
		existing  []client.Object
		ctc       *conveyorv1alpha1.ClusterTriggerClass
		body      string
		wantCode  int
		wantDup   string
		wantTasks int
	}{
		{
			name:      "active Task of the same class and issue blocks",
			existing:  []client.Object{existingTask("gh-run", "gh-issues", "42", conveyorv1alpha1.TaskPhaseRunning)},
			ctc:       dedupeTriggerClass("gh-issues"),
			body:      issue42,
			wantCode:  http.StatusAccepted,
			wantDup:   "gh-run",
			wantTasks: 1,
		},
		{
			name:      "Task with an empty phase counts as active",
			existing:  []client.Object{existingTask("gh-new", "gh-issues", "42", "")},
			ctc:       dedupeTriggerClass("gh-issues"),
			body:      issue42,
			wantCode:  http.StatusAccepted,
			wantDup:   "gh-new",
			wantTasks: 1,
		},
		{
			name: "terminal Tasks never block",
			existing: []client.Object{
				existingTask("gh-done", "gh-issues", "42", conveyorv1alpha1.TaskPhaseCompleted),
				existingTask("gh-fail", "gh-issues", "42", conveyorv1alpha1.TaskPhaseFailed),
				existingTask("gh-late", "gh-issues", "42", conveyorv1alpha1.TaskPhaseTimedOut),
			},
			ctc:       dedupeTriggerClass("gh-issues"),
			body:      issue42,
			wantCode:  http.StatusOK,
			wantTasks: 4,
		},
		{
			name:      "active Task for another issue does not block",
			existing:  []client.Object{existingTask("gh-other", "gh-issues", "7", conveyorv1alpha1.TaskPhaseRunning)},
			ctc:       dedupeTriggerClass("gh-issues"),
			body:      issue42,
			wantCode:  http.StatusOK,
			wantTasks: 2,
		},
		{
			name:      "two classes do not collide on the same issue",
			existing:  []client.Object{existingTask("gh-run", "gh-issues", "42", conveyorv1alpha1.TaskPhaseRunning)},
			ctc:       dedupeTriggerClass("gh-pr-iterate"),
			body:      issue42,
			wantCode:  http.StatusOK,
			wantTasks: 2,
		},
		{
			name:      "each class dedupes its own Tasks",
			existing:  []client.Object{existingTask("gh-iter", "gh-pr-iterate", "42", conveyorv1alpha1.TaskPhasePending)},
			ctc:       dedupeTriggerClass("gh-pr-iterate"),
			body:      issue42,
			wantCode:  http.StatusAccepted,
			wantDup:   "gh-iter",
			wantTasks: 1,
		},
		{
			name:      "missing uniqueBy label skips dedupe",
			existing:  []client.Object{existingTask("gh-run", "gh-issues", "42", conveyorv1alpha1.TaskPhaseRunning)},
			ctc:       dedupeTriggerClass("gh-issues"),
			body:      `{"action":"opened","issue":{"title":"no number"}}`,
			wantCode:  http.StatusOK,
			wantTasks: 2,
		},
		{
			name:     "class without dedupe keeps creating Tasks",
			existing: []client.Object{existingTask("gh-run", "gh-issues", "42", conveyorv1alpha1.TaskPhaseRunning)},
			ctc: func() *conveyorv1alpha1.ClusterTriggerClass {
				c := dedupeTriggerClass("gh-issues")
				c.Spec.Dedupe = nil
				return c
			}(),
			body:      issue42,
			wantCode:  http.StatusOK,
			wantTasks: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append([]client.Object{tt.ctc, webhookSecret()}, tt.existing...)
			c := newFakeClient(t, objs...)
			h := newHandler(c)
			h.Reader = c

			rec := deliver(t, h, tt.ctc.Spec.Path, tt.body)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d (body=%s), want %d", rec.Code, rec.Body.String(), tt.wantCode)
			}
			if tt.wantDup != "" {
				var resp map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp["duplicate"] != true || resp["task"] != tt.wantDup {
					t.Errorf("response = %v, want duplicate of %q", resp, tt.wantDup)
				}
			}
			if got := countTasks(t, c); got != tt.wantTasks {
				t.Errorf("tasks after delivery = %d, want %d", got, tt.wantTasks)
			}
		})
	}
}

func TestDedupe_StampsTheTriggerClassOverAMapping(t *testing.T) {
	ctc := dedupeTriggerClass("gh-issues")
	// A hostile or careless mapping tries to move the Task into another class.
	ctc.Spec.Task.Mappings = append(ctc.Spec.Task.Mappings,
		conveyorv1alpha1.FieldMapping{From: "issue.title", To: "labels." + policy.LabelTriggerClass})
	c := newFakeClient(t, ctc, webhookSecret())
	h := newHandler(c)

	if rec := deliver(t, h, ctc.Spec.Path, issue42); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s), want 200", rec.Code, rec.Body.String())
	}
	var list conveyorv1alpha1.TaskList
	if err := c.List(context.Background(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list tasks: %v, %d items", err, len(list.Items))
	}
	if got := list.Items[0].Labels[policy.LabelTriggerClass]; got != "gh-issues" {
		t.Errorf("trigger-class label = %q, want gh-issues", got)
	}
}
