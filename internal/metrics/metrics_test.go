/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Metrics registered via promauto live in a process-global registry, so
// increments from other tests in the same binary can leak in. Each test
// here uses a fresh label combination so reads are deterministic.

func TestObservePhaseTransition(t *testing.T) {
	ObservePhaseTransition("Completed", "Completed")
	ObservePhaseTransition("Completed", "Completed")

	got := testutil.ToFloat64(TaskPhaseTransitions.WithLabelValues("Completed", "Completed"))
	if got != 2 {
		t.Fatalf("phase transition counter: got %v, want 2", got)
	}
}

func TestObserveReconcileError(t *testing.T) {
	ObserveReconcileError(StepJob)

	got := testutil.ToFloat64(TaskReconcileErrors.WithLabelValues(StepJob))
	if got != 1 {
		t.Fatalf("reconcile error counter: got %v, want 1", got)
	}
}

func TestObserveTaskDuration(t *testing.T) {
	ObserveTaskDuration("Failed", 12.5)
	ObserveTaskDuration("Failed", 30.0)

	// CollectAndCount on the HistogramVec returns the number of distinct
	// label combinations currently tracked — one child per phase observed.
	got := testutil.CollectAndCount(TaskDurationSeconds)
	if got < 1 {
		t.Fatalf("duration histogram stream count: got %v, want >= 1", got)
	}
}

func TestObserveWebhook(t *testing.T) {
	ObserveWebhook("gh-issues", ResultAccepted)
	ObserveWebhook("gh-issues", ResultFiltered)
	ObserveWebhook("gh-issues", ResultAccepted)

	accepted := testutil.ToFloat64(WebhookRequests.WithLabelValues("gh-issues", ResultAccepted))
	filtered := testutil.ToFloat64(WebhookRequests.WithLabelValues("gh-issues", ResultFiltered))
	if accepted != 2 || filtered != 1 {
		t.Fatalf("webhook counter: accepted=%v filtered=%v, want 2/1", accepted, filtered)
	}
}
