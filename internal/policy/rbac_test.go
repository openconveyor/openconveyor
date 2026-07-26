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

func TestBuildRole(t *testing.T) {
	tests := []struct {
		name     string
		taskName string
		rules    []conveyorv1alpha1.RBACRule
		wantNil  bool
		wantRule *rbacv1.PolicyRule // checked when wantNil is false
	}{
		{
			name:     "nil rules → nil role",
			taskName: "t",
			rules:    nil,
			wantNil:  true,
		},
		{
			name:     "empty-slice rules → nil role",
			taskName: "t",
			rules:    []conveyorv1alpha1.RBACRule{},
			wantNil:  true,
		},
		{
			name:     "copies rules verbatim",
			taskName: demoTaskName,
			rules: []conveyorv1alpha1.RBACRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"configmaps"},
					Verbs:         []string{"get", "list"},
					ResourceNames: []string{"app-config"},
				},
			},
			wantRule: &rbacv1.PolicyRule{
				APIGroups:     []string{""},
				Resources:     []string{"configmaps"},
				Verbs:         []string{"get", "list"},
				ResourceNames: []string{"app-config"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			role := BuildRole(tc.taskName, "ns", tc.rules)
			if tc.wantNil {
				if role != nil {
					t.Errorf("expected nil for empty rules, got %+v", role)
				}
				return
			}
			if role == nil {
				t.Fatal("expected role, got nil")
			}
			if role.Name != tc.taskName || role.Namespace != "ns" {
				t.Errorf("metadata: got %s/%s want ns/%s", role.Namespace, role.Name, tc.taskName)
			}
			if len(role.Rules) != 1 {
				t.Fatalf("rule count: got %d want 1", len(role.Rules))
			}
			if !rulesEqual(role.Rules[0], *tc.wantRule) {
				t.Errorf("rule: got %+v want %+v", role.Rules[0], *tc.wantRule)
			}
			if role.Labels[LabelManagedBy] != ManagedByValue {
				t.Errorf("managed-by label missing")
			}
		})
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
	tests := []struct {
		name  string
		rules []conveyorv1alpha1.RBACRule
		want  bool
	}{
		{
			name:  "nil rules should not need token",
			rules: nil,
			want:  false,
		},
		{
			name:  "any rule should flip token on",
			rules: []conveyorv1alpha1.RBACRule{{Verbs: []string{"get"}}},
			want:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsServiceAccountToken(tc.rules); got != tc.want {
				t.Errorf("NeedsServiceAccountToken: got %v want %v", got, tc.want)
			}
		})
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
