/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package metrics registers and exposes OpenConveyor's custom Prometheus
// metrics against controller-runtime's shared registry. The manager's
// built-in metrics server (see cmd/main.go) serves them at /metrics.
//
// All counters/histograms are package-level so callers just import the
// package and call the typed helpers — no handle to pass around.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// ResultAccepted means the webhook matched a ClusterTriggerClass, passed
	// signature + filters, and a Task was created.
	ResultAccepted = "accepted"
	// ResultFiltered means the request matched a CTC and signature was valid
	// but filters excluded it — explicitly not an error.
	ResultFiltered = "filtered"
	// ResultRejectedSignature covers missing or mismatched HMAC.
	ResultRejectedSignature = "rejected_signature"
	// ResultRejectedMethod is a non-POST request.
	ResultRejectedMethod = "rejected_method"
	// ResultRejectedBodyTooLarge is body > maxBodyBytes.
	ResultRejectedBodyTooLarge = "rejected_body_too_large"
	// ResultRejectedBadBody covers unreadable or non-JSON bodies.
	ResultRejectedBadBody = "rejected_bad_body"
	// ResultNotFound is a path that matches no CTC.
	ResultNotFound = "not_found"
	// ResultBuildFailed means mappings could not produce a valid Task.
	ResultBuildFailed = "build_failed"
	// ResultError is any server-side failure (apiserver create, lookup, …).
	ResultError = "error"
)

const (
	// StepValidate covers Task spec validation.
	StepValidate = "validate"
	// StepAgentClassLookup covers ClusterAgentClass Get.
	StepAgentClassLookup = "agent_class_lookup"
	// StepServiceAccount covers SA ensure.
	StepServiceAccount = "service_account"
	// StepRole covers Role ensure.
	StepRole = "role"
	// StepRoleBinding covers RoleBinding ensure.
	StepRoleBinding = "role_binding"
	// StepNetworkPolicy covers NetworkPolicy ensure.
	StepNetworkPolicy = "network_policy"
	// StepPromptConfigMap covers inline-prompt ConfigMap ensure.
	StepPromptConfigMap = "prompt_configmap"
	// StepJob covers Job ensure.
	StepJob = "job"
	// StepStatus covers Status subresource update.
	StepStatus = "status"
)

var (
	// TaskPhaseTransitions counts every observed phase change. A Task that
	// goes Pending → Running → Completed contributes three increments (one
	// per transition). Labels:
	//   - phase:  the new phase (Pending/Running/Completed/Failed/TimedOut)
	//   - reason: the condition reason driving the transition
	TaskPhaseTransitions = promauto.With(ctrlmetrics.Registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "conveyor_task_phase_transitions_total",
			Help: "Count of Task phase transitions observed by the controller.",
		},
		[]string{"phase", "reason"},
	)

	// TaskReconcileErrors counts reconcile-step failures. Label:
	//   - step: the reconcile step that failed (see Step* constants)
	TaskReconcileErrors = promauto.With(ctrlmetrics.Registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "conveyor_task_reconcile_errors_total",
			Help: "Count of Task reconcile failures, labelled by step.",
		},
		[]string{"step"},
	)

	// TaskDurationSeconds observes total wall-clock duration from Job start
	// to Job completion once a Task reaches a terminal phase. Label:
	//   - phase: terminal phase (Completed/Failed/TimedOut)
	TaskDurationSeconds = promauto.With(ctrlmetrics.Registry).NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "conveyor_task_duration_seconds",
			Help:    "Wall-clock duration of a Task's Job, observed at terminal phase.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 12), // 1s .. ~68min
		},
		[]string{"phase"},
	)

	// WebhookRequests counts every webhook request the trigger adapter
	// processes, regardless of outcome. Labels:
	//   - trigger_class: the matched CTC name, or "" when no match
	//   - result:        terminal result for the request (see Result* constants)
	WebhookRequests = promauto.With(ctrlmetrics.Registry).NewCounterVec(
		prometheus.CounterOpts{
			Name: "conveyor_webhook_requests_total",
			Help: "Count of webhook requests processed by the trigger adapter.",
		},
		[]string{"trigger_class", "result"},
	)
)

// ObservePhaseTransition records a phase change.
func ObservePhaseTransition(phase, reason string) {
	TaskPhaseTransitions.WithLabelValues(phase, reason).Inc()
}

// ObserveReconcileError records a reconcile-step failure.
func ObserveReconcileError(step string) {
	TaskReconcileErrors.WithLabelValues(step).Inc()
}

// ObserveTaskDuration records total task wall-clock time at terminal phase.
func ObserveTaskDuration(phase string, seconds float64) {
	TaskDurationSeconds.WithLabelValues(phase).Observe(seconds)
}

// ObserveWebhook records a webhook request outcome.
func ObserveWebhook(triggerClass, result string) {
	WebhookRequests.WithLabelValues(triggerClass, result).Inc()
}
