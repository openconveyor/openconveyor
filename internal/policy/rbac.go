/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	rbacv1 "k8s.io/api/rbac/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// BuildRole returns a namespaced Role that carries the verbs the Task
// declared, or nil if the Task declared no rules. Nil means the caller must
// delete any previous Role and RoleBinding for this Task, so a rolled-back
// spec cannot leave permissions behind.
//
// RBAC is opt-in: an empty spec.permissions.rbac means no SA token and no
// Role. The function copies all other rules verbatim. The API server
// validates them when the controller applies the Role.
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
		ObjectMeta: ownedMeta(taskName, namespace, taskName),
		Rules:      policyRules,
	}
}

// BuildRoleBinding ties the ServiceAccount of the Task to its Role. It
// returns nil if rules is empty, symmetric with BuildRole, so the controller
// creates or deletes both resources together.
func BuildRoleBinding(taskName, namespace string, rules []conveyorv1alpha1.RBACRule) *rbacv1.RoleBinding {
	if len(rules) == 0 {
		return nil
	}

	return &rbacv1.RoleBinding{
		ObjectMeta: ownedMeta(taskName, namespace, taskName),
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

// NeedsServiceAccountToken reports whether the Job mounts a token. The
// baseline is false; any declared RBAC rule turns it on. This function is
// the single source of truth, so build.go and the policy package cannot
// drift apart.
func NeedsServiceAccountToken(rules []conveyorv1alpha1.RBACRule) bool {
	return len(rules) > 0
}
