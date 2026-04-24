/*
Copyright 2026.
*/

package policy

import (
	"testing"
)

func TestPodSelectorLabels(t *testing.T) {
	labels := PodSelectorLabels("my-task")
	if labels[LabelTaskName] != "my-task" {
		t.Errorf("task label = %q, want my-task", labels[LabelTaskName])
	}
	if len(labels) != 1 {
		t.Errorf("expected 1 label, got %d", len(labels))
	}
}

func TestOwnershipLabels(t *testing.T) {
	labels := OwnershipLabels("my-task")
	if labels[LabelManagedBy] != ManagedByValue {
		t.Errorf("managed-by = %q, want %q", labels[LabelManagedBy], ManagedByValue)
	}
	if labels[LabelTaskName] != "my-task" {
		t.Errorf("task label = %q, want my-task", labels[LabelTaskName])
	}
	if len(labels) != 2 {
		t.Errorf("expected 2 labels, got %d", len(labels))
	}
}

func TestMergeStringSets(t *testing.T) {
	cases := []struct {
		name  string
		lists [][]string
		want  []string
	}{
		{
			name: "nil lists",
			want: []string{},
		},
		{
			name:  "single list",
			lists: [][]string{{"a", "b"}},
			want:  []string{"a", "b"},
		},
		{
			name:  "deduplicates",
			lists: [][]string{{"a", "b"}, {"b", "c"}},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "drops empties",
			lists: [][]string{{"a", "", "b"}, {"", "c"}},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "preserves insertion order",
			lists: [][]string{{"z", "a"}, {"m", "a"}},
			want:  []string{"z", "a", "m"},
		},
		{
			name:  "all empty strings",
			lists: [][]string{{"", ""}, {""}},
			want:  []string{},
		},
		{
			name:  "empty slices",
			lists: [][]string{{}, {}},
			want:  []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeStringSets(tc.lists...)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d; got %v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
