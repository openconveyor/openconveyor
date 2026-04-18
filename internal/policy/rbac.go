/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// BuildRole returns a namespaced Role carrying the verbs the Task declared,
// or nil if the Task asked for nothing. Nil means the caller should delete
// any prior Role + RoleBinding for this Task so a rolled-back spec cannot
// leave permissions behind.
//
// RBAC is opt-in: empty spec.permissions.rbac → no SA token, no Role.
// Anything else flows through verbatim, with validation left to the API
// server when the Role is applied.
func BuildRole(taskName, namespace string, rules []conveyorv1alpha1.RBACRule) *rbacv1.Role {
	if len(rules) == 0 {
		return nil
	}

	policyRules := make([]rbacv1.PolicyRule, 0, len(rules))
	for _, r := range rules {
		policyRules = append(policyRules, rbacv1.PolicyRule{
			APIGroups:     r.APIGroups,
			Resources:     r.Resources,
			Verbs:         r.Verbs,
			ResourceNames: r.ResourceNames,
		})
	}

	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: namespace,
			Labels:    OwnershipLabels(taskName),
		},
		Rules: policyRules,
	}
}

// BuildRoleBinding ties the Task's ServiceAccount to its Role. Returns nil
// if rules is empty — symmetric with BuildRole so both resources live or
// die together.
func BuildRoleBinding(taskName, namespace string, rules []conveyorv1alpha1.RBACRule) *rbacv1.RoleBinding {
	if len(rules) == 0 {
		return nil
	}

	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskName,
			Namespace: namespace,
			Labels:    OwnershipLabels(taskName),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      taskName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     taskName,
		},
	}
}

// NeedsServiceAccountToken reports whether the Job should mount a token.
// Baseline is false; declaring any RBAC rule flips it on. This is the
// single source of truth so build.go and the policy package can't drift.
func NeedsServiceAccountToken(rules []conveyorv1alpha1.RBACRule) bool {
	return len(rules) > 0
}
