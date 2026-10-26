/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package controller

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// validateTask is the in-reconciler backstop behind the CRD's CEL rules
// (api/v1alpha1/task_types.go). CEL rejects invalid specs at admission.
// This function catches objects that predate those rules or that bypassed
// them.
func validateTask(task *conveyorv1alpha1.Task) error {
	if task.Spec.Resources.Timeout.Duration <= 0 {
		return errors.New("spec.resources.timeout must be > 0")
	}
	if task.Spec.Agent.Ref == "" {
		return errors.New("spec.agent.ref is required")
	}
	p := task.Spec.Prompt
	sources := 0
	if p.Inline != "" {
		sources++
	}
	if p.SecretRef != nil {
		sources++
	}
	if p.ConfigMapRef != nil {
		sources++
	}
	if sources != 1 {
		return errors.New("spec.prompt requires exactly one of inline/secretRef/configMapRef")
	}
	if r := task.Spec.Resources; r.CPU != "" {
		if _, err := resource.ParseQuantity(r.CPU); err != nil {
			return fmt.Errorf("spec.resources.cpu: %w", err)
		}
	}
	if r := task.Spec.Resources; r.Memory != "" {
		if _, err := resource.ParseQuantity(r.Memory); err != nil {
			return fmt.Errorf("spec.resources.memory: %w", err)
		}
	}
	return nil
}
