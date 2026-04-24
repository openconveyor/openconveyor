/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
	"github.com/openconveyor/openconveyor/internal/metrics"
)

// maxBodyBytes caps inbound webhook payloads. 1 MiB covers everything
// the GitHub / GitLab / Linear webhooks send in practice and keeps a
// misbehaving (or malicious) source from exhausting memory.
const maxBodyBytes = 1 << 20

// Handler answers webhook POSTs by looking up a matching
// ClusterTriggerClass, verifying the HMAC signature with the referenced
// Secret, and materialising a Task from the mapped payload.
//
// One Handler per process; routes are discovered dynamically from the
// cluster so new ClusterTriggerClass objects take effect without a restart.
// The Client is expected to be cache-backed (mgr.GetClient()) so reads
// during high-volume webhook bursts stay in-process.
type Handler struct {
	Client    client.Client
	Namespace string // namespace where HMAC Secrets live (usually the adapter's)
	Log       logr.Logger
}

// ServeHTTP validates and dispatches a single webhook request.
//
//	POST /<spec.path>    body = webhook JSON    header = configured signature
//
// Responses:
//
//	200 Task created (body: {"task": "<name>"})
//	202 matched but filtered out (body: {"filtered": true})
//	400 malformed payload / no prompt produced
//	401 missing or mismatched signature
//	404 no ClusterTriggerClass registered for this path
//	405 non-POST verb
//	413 body larger than maxBodyBytes
//	500 server error (bad Secret lookup, apiserver create failure)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		metrics.ObserveWebhook("", metrics.ResultRejectedMethod)
		writeStatus(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			metrics.ObserveWebhook("", metrics.ResultRejectedBodyTooLarge)
			writeStatus(w, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		metrics.ObserveWebhook("", metrics.ResultRejectedBadBody)
		writeStatus(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	defer func() { _ = r.Body.Close() }()

	if !json.Valid(body) {
		metrics.ObserveWebhook("", metrics.ResultRejectedBadBody)
		writeStatus(w, http.StatusBadRequest, "body is not valid JSON")
		return
	}

	ctc, err := h.findTriggerClass(r.Context(), r.URL.Path)
	if err != nil {
		metrics.ObserveWebhook("", metrics.ResultError)
		writeStatus(w, http.StatusInternalServerError, "lookup trigger class: "+err.Error())
		return
	}
	if ctc == nil {
		metrics.ObserveWebhook("", metrics.ResultNotFound)
		writeStatus(w, http.StatusNotFound, "no ClusterTriggerClass registered for path "+r.URL.Path)
		return
	}

	secret, err := h.lookupSignatureSecret(r.Context(), ctc)
	if err != nil {
		h.Log.Error(err, "Failed to load signature secret", "triggerClass", ctc.Name)
		metrics.ObserveWebhook(ctc.Name, metrics.ResultError)
		writeStatus(w, http.StatusInternalServerError, "load signature secret")
		return
	}

	header := r.Header.Get(ctc.Spec.Signature.Header)
	if err := VerifyHMAC(ctc.Spec.Signature.Algorithm, ctc.Spec.Signature.Prefix, header, secret, body); err != nil {
		h.Log.V(1).Info("Rejected signature", "triggerClass", ctc.Name, "err", err.Error())
		metrics.ObserveWebhook(ctc.Name, metrics.ResultRejectedSignature)
		writeStatus(w, http.StatusUnauthorized, "signature verification failed")
		return
	}

	if !ApplyFilters(body, ctc.Spec.Filters) {
		metrics.ObserveWebhook(ctc.Name, metrics.ResultFiltered)
		writeJSON(w, http.StatusAccepted, map[string]any{"filtered": true})
		return
	}

	task, err := BuildTask(ctc.Spec.Task, body)
	if err != nil {
		metrics.ObserveWebhook(ctc.Name, metrics.ResultBuildFailed)
		writeStatus(w, http.StatusBadRequest, "build task: "+err.Error())
		return
	}
	// Default the emitted Task's namespace to the adapter's own when the
	// template left it unset — avoids accidentally dumping Tasks into
	// "default" on clusters where that namespace is unused.
	if task.Namespace == "" {
		task.Namespace = h.Namespace
	}

	if err := h.Client.Create(r.Context(), task); err != nil {
		h.Log.Error(err, "Failed to create Task", "triggerClass", ctc.Name)
		metrics.ObserveWebhook(ctc.Name, metrics.ResultError)
		writeStatus(w, http.StatusInternalServerError, "create task: "+err.Error())
		return
	}

	h.Log.Info("Created Task from webhook", "triggerClass", ctc.Name, "task", task.Name, "namespace", task.Namespace)
	metrics.ObserveWebhook(ctc.Name, metrics.ResultAccepted)
	writeJSON(w, http.StatusOK, map[string]any{"task": task.Name, "namespace": task.Namespace})
}

// findTriggerClass looks up the ClusterTriggerClass whose spec.path
// matches the request path exactly. Ambiguous paths (two CTCs with the
// same path) are rejected — easier to fix the config than to guess.
func (h *Handler) findTriggerClass(ctx context.Context, path string) (*conveyorv1alpha1.ClusterTriggerClass, error) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	var list conveyorv1alpha1.ClusterTriggerClassList
	if err := h.Client.List(ctx, &list); err != nil {
		return nil, err
	}
	var matches []conveyorv1alpha1.ClusterTriggerClass
	for _, ctc := range list.Items {
		p := strings.TrimRight(ctc.Spec.Path, "/")
		if p == "" {
			p = "/"
		}
		if p == path {
			matches = append(matches, ctc)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("path %q is claimed by %d ClusterTriggerClasses", path, len(matches))
	}
}

func (h *Handler) lookupSignatureSecret(ctx context.Context, ctc *conveyorv1alpha1.ClusterTriggerClass) ([]byte, error) {
	ref := ctc.Spec.Signature.SecretRef
	if ref.Name == "" || ref.Key == "" {
		return nil, fmt.Errorf("signature.secretRef is incomplete")
	}
	var secret corev1.Secret
	key := types.NamespacedName{Name: ref.Name, Namespace: h.Namespace}
	if err := h.Client.Get(ctx, key, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s/%s not found", key.Namespace, key.Name)
		}
		return nil, err
	}
	val, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s has no key %q", key.Namespace, key.Name, ref.Key)
	}
	return val, nil
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}
