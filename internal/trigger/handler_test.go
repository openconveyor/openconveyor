/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

const adapterNamespace = "conveyor-system"

func newFakeClient(t *testing.T, initial ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := conveyorv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add conveyor scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(initial...).Build()
}

func newHandler(c client.Client) *Handler {
	return &Handler{Client: c, Namespace: adapterNamespace, Log: logr.Discard()}
}

// githubTriggerClass returns a realistic ClusterTriggerClass for a GitHub
// issue-opened webhook. Used by several cases.
func githubTriggerClass() *conveyorv1alpha1.ClusterTriggerClass {
	return &conveyorv1alpha1.ClusterTriggerClass{
		ObjectMeta: metav1.ObjectMeta{Name: "github-issues"},
		Spec: conveyorv1alpha1.ClusterTriggerClassSpec{
			Path: "/github",
			Signature: conveyorv1alpha1.WebhookSignature{
				Header:    "X-Hub-Signature-256",
				Algorithm: "sha256",
				Prefix:    "sha256=",
				SecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "github-webhook"},
					Key:                  "secret",
				},
			},
			Filters: []conveyorv1alpha1.WebhookFilter{
				{Path: "action", Equals: "opened"},
			},
			Task: conveyorv1alpha1.TaskTemplate{
				Namespace:          "conveyor-tasks",
				GenerateNamePrefix: "gh-",
				Agent:              conveyorv1alpha1.AgentRef{Ref: "claude-code-implementer"},
				Resources:          conveyorv1alpha1.TaskResources{Timeout: metav1.Duration{Duration: 30 * time.Minute}},
				Mappings: []conveyorv1alpha1.FieldMapping{
					{From: "issue.title", To: "prompt"},
				},
			},
		},
	}
}

func webhookSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: adapterNamespace},
		Data:       map[string][]byte{"secret": []byte("shh")},
	}
}

// post builds a POST request with the body + the GitHub HMAC signature header.
func post(t *testing.T, path string, body []byte, sigValue string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	r.Header.Set("X-Hub-Signature-256", sigValue)
	return r.WithContext(context.Background())
}

func TestHandler_HappyPath(t *testing.T) {
	ctc := githubTriggerClass()
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"fix null deref"}}`)
	sig := "sha256=" + hmacHex(secret.Data["secret"], body)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, sig))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s), want 200", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["namespace"] != "conveyor-tasks" {
		t.Errorf("namespace = %q, want conveyor-tasks", resp["namespace"])
	}

	var list conveyorv1alpha1.TaskList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("task count = %d, want 1", len(list.Items))
	}
	task := list.Items[0]
	if task.Spec.Prompt.Inline != "fix null deref" {
		t.Errorf("prompt = %q, want \"fix null deref\"", task.Spec.Prompt.Inline)
	}
	if !strings.HasPrefix(task.Name, "gh-") {
		t.Errorf("name %q missing generateName prefix", task.Name)
	}
}

func TestHandler_BadSignature(t *testing.T) {
	ctc := githubTriggerClass()
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"x"}}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, "sha256=deadbeef"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	var list conveyorv1alpha1.TaskList
	_ = c.List(context.Background(), &list)
	if len(list.Items) != 0 {
		t.Fatalf("task created despite bad signature: %+v", list.Items)
	}
}

func TestHandler_FilterMismatchAccepts(t *testing.T) {
	ctc := githubTriggerClass()
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"closed","issue":{"title":"irrelevant"}}`)
	sig := "sha256=" + hmacHex(secret.Data["secret"], body)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, sig))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	var list conveyorv1alpha1.TaskList
	_ = c.List(context.Background(), &list)
	if len(list.Items) != 0 {
		t.Fatalf("task created despite filter mismatch")
	}
}

func TestHandler_UnknownPath(t *testing.T) {
	c := newFakeClient(t)
	h := newHandler(c)
	body := []byte(`{}`)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/nope", body, "sha256=abc"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandler_NonPostRejected(t *testing.T) {
	c := newFakeClient(t, githubTriggerClass(), webhookSecret())
	h := newHandler(c)
	r := httptest.NewRequest(http.MethodGet, "/github", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandler_InvalidJSON(t *testing.T) {
	c := newFakeClient(t, githubTriggerClass(), webhookSecret())
	h := newHandler(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", []byte(`not json`), "sha256=abc"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_SecretMissing(t *testing.T) {
	// CTC references a Secret the handler will not find.
	ctc := githubTriggerClass()
	c := newFakeClient(t, ctc) // no Secret
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"x"}}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, "sha256=abc"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestHandler_BodyTooLarge(t *testing.T) {
	ctc := githubTriggerClass()
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	// 1 MiB + 1 byte exceeds maxBodyBytes.
	big := make([]byte, maxBodyBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	r := httptest.NewRequest(http.MethodPost, "/github", bytes.NewReader(big))
	r.Header.Set("X-Hub-Signature-256", "sha256=irrelevant")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHandler_AmbiguousPath(t *testing.T) {
	ctc1 := githubTriggerClass()
	ctc2 := githubTriggerClass()
	ctc2.Name = "github-issues-2" // same path, different name
	secret := webhookSecret()
	c := newFakeClient(t, ctc1, ctc2, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"x"}}`)
	sig := "sha256=" + hmacHex(secret.Data["secret"], body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, sig))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for ambiguous path", rec.Code)
	}
}

func TestHandler_IncompleteSecretRef(t *testing.T) {
	ctc := githubTriggerClass()
	ctc.Spec.Signature.SecretRef.Key = "" // incomplete
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"x"}}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, "sha256=abc"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for incomplete secretRef", rec.Code)
	}
}

func TestHandler_SecretKeyMissing(t *testing.T) {
	ctc := githubTriggerClass()
	// Secret exists but with a different key than expected.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-webhook", Namespace: adapterNamespace},
		Data:       map[string][]byte{"wrong-key": []byte("shh")},
	}
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"x"}}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github", body, "sha256=abc"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for missing secret key", rec.Code)
	}
}

func TestHandler_TrailingSlashNormalized(t *testing.T) {
	ctc := githubTriggerClass()
	secret := webhookSecret()
	c := newFakeClient(t, ctc, secret)
	h := newHandler(c)

	body := []byte(`{"action":"opened","issue":{"title":"fix it"}}`)
	sig := "sha256=" + hmacHex(secret.Data["secret"], body)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, post(t, "/github/", body, sig))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s), want 200 for trailing-slash path", rec.Code, rec.Body.String())
	}
}
