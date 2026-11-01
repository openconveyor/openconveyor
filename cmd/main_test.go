/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package main

import (
	"math"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/labels"
)

func TestValidateJobTTL(t *testing.T) {
	for _, v := range []int{1, 3600, math.MaxInt32} {
		if err := validateJobTTL(v); err != nil {
			t.Errorf("validateJobTTL(%d) = %v, want nil", v, err)
		}
	}
	for _, v := range []int{0, -1, math.MaxInt32 + 1} {
		err := validateJobTTL(v)
		if err == nil {
			t.Errorf("validateJobTTL(%d) = nil, want an error", v)
			continue
		}
		if v == 0 && !strings.Contains(err.Error(), "Result") {
			t.Errorf("validateJobTTL(0) error %q does not say why 0 is refused", err)
		}
	}
}

func TestTaskPodSelector(t *testing.T) {
	sel := taskPodSelector()
	cases := []struct {
		labels map[string]string
		want   bool
	}{
		{map[string]string{"openconveyor.ai/task": "t", "app.kubernetes.io/managed-by": "conveyor"}, true},
		{map[string]string{"openconveyor.ai/task": "t"}, false},
		{map[string]string{"app.kubernetes.io/managed-by": "conveyor"}, false},
		{map[string]string{"app": "web"}, false},
	}
	for _, c := range cases {
		if got := sel.Matches(labels.Set(c.labels)); got != c.want {
			t.Errorf("selector %q on %v = %v, want %v", sel, c.labels, got, c.want)
		}
	}
}
