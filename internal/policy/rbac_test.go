/*
Copyright 2026.
*/

package policy

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

const demoTaskName = "demo"

func TestBuildRole_NilWhenEmpty(t *testing.T) {
	if got := BuildRole("t", "ns", nil); got != nil {
		t.Errorf("expected nil for empty rules, got %+v", got)
	}
	if got := BuildRole("t", "ns", []conveyorv1alpha1.RBACRule{}); got != nil {
		t.Errorf("expected nil for empty-slice rules, got %+v", got)
	}
}

func TestBuildRole_CopiesRulesVerbatim(t *testing.T) {
	rules := []conveyorv1alpha1.RBACRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"configmaps"},
			Verbs:         []string{"get", "list"},
			ResourceNames: []string{"app-config"},
		},
	}
	role := BuildRole(demoTaskName, "ns", rules)
	if role == nil {
		t.Fatal("expected role, got nil")
	}
	if role.Name != demoTaskName || role.Namespace != "ns" {
		t.Errorf("metadata: got %s/%s want ns/demo", role.Namespace, role.Name)
	}
	if len(role.Rules) != 1 {
		t.Fatalf("rule count: got %d want 1", len(role.Rules))
	}
	got := role.Rules[0]
	want := rbacv1.PolicyRule{
		APIGroups:     []string{""},
		Resources:     []string{"configmaps"},
		Verbs:         []string{"get", "list"},
		ResourceNames: []string{"app-config"},
	}
	if !rulesEqual(got, want) {
		t.Errorf("rule: got %+v want %+v", got, want)
	}
	if role.Labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("managed-by label missing")
	}
}

func TestBuildRoleBinding_SubjectPointsAtTaskSA(t *testing.T) {
	rules := []conveyorv1alpha1.RBACRule{
		{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
	}
	rb := BuildRoleBinding(demoTaskName, "ns", rules)
	if rb == nil {
		t.Fatal("expected rolebinding, got nil")
	}
	if len(rb.Subjects) != 1 {
		t.Fatalf("subject count: got %d want 1", len(rb.Subjects))
	}
	s := rb.Subjects[0]
	if s.Kind != rbacv1.ServiceAccountKind || s.Name != demoTaskName || s.Namespace != "ns" {
		t.Errorf("subject: got %+v", s)
	}
	if rb.RoleRef.Name != demoTaskName || rb.RoleRef.Kind != "Role" {
		t.Errorf("roleRef: got %+v", rb.RoleRef)
	}
}

func TestNeedsServiceAccountToken(t *testing.T) {
	if NeedsServiceAccountToken(nil) {
		t.Error("nil rules should not need token")
	}
	if !NeedsServiceAccountToken([]conveyorv1alpha1.RBACRule{{Verbs: []string{"get"}}}) {
		t.Error("any rule should flip token on")
	}
}

func rulesEqual(a, b rbacv1.PolicyRule) bool {
	return slicesEqual(a.APIGroups, b.APIGroups) &&
		slicesEqual(a.Resources, b.Resources) &&
		slicesEqual(a.Verbs, b.Verbs) &&
		slicesEqual(a.ResourceNames, b.ResourceNames)
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
