/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

func TestParseResult(t *testing.T) {
	allow := []string{"github.com", "api.github.com", "Example.ORG.", "203.0.113.0/24", "2001:db8::1"}
	prURL := "https://github.com/acme/repo/pull/7"

	tests := []struct {
		name    string
		msg     string
		outcome ResultOutcome
		want    *conveyorv1alpha1.TaskResult
		dropped int
	}{
		{
			name:    "empty message",
			msg:     "",
			outcome: ResultNotProvided,
		},
		{
			name:    "kubelet read error has no JSON object",
			msg:     "Error on reading termination log /dev/termination-log: permission denied",
			outcome: ResultNotProvided,
		},
		{
			name:    "empty object",
			msg:     "{}",
			outcome: ResultNotProvided,
		},
		{
			name:    "summary and links",
			msg:     `{"summary":"Opened a PR","links":[{"name":"pull-request","url":"` + prURL + `"},{"name":"issue","url":"https://github.com/acme/repo/issues/3"}]}`,
			outcome: ResultRecorded,
			want: &conveyorv1alpha1.TaskResult{Summary: "Opened a PR", Links: []conveyorv1alpha1.ResultLink{
				{Name: "pull-request", URL: prURL},
				{Name: "issue", URL: "https://github.com/acme/repo/issues/3"},
			}},
		},
		{
			name:    "runtime prefix before the object",
			msg:     `Completed: {"summary":"done"}`,
			outcome: ResultRecorded,
			want:    &conveyorv1alpha1.TaskResult{Summary: "done"},
		},
		{
			name:    "trailing newline is accepted",
			msg:     "{\"summary\":\"done\"}\n",
			outcome: ResultRecorded,
			want:    &conveyorv1alpha1.TaskResult{Summary: "done"},
		},
		{
			name:    "bad JSON",
			msg:     `{"summary":"done"`,
			outcome: ResultInvalid,
		},
		{
			name:    "unknown field",
			msg:     `{"summary":"done","token":"ghp_x"}`,
			outcome: ResultInvalid,
		},
		{
			name:    "wrong type",
			msg:     `{"summary":42}`,
			outcome: ResultInvalid,
		},
		{
			name:    "data after the object",
			msg:     `{"summary":"a"}{"summary":"b"}`,
			outcome: ResultInvalid,
		},
		{
			name:    "object larger than the contract",
			msg:     `{"summary":"` + strings.Repeat("a", MaxResultBytes) + `"}`,
			outcome: ResultInvalid,
		},
		{
			name:    "host not in the allowlist is dropped",
			msg:     `{"summary":"x","links":[{"name":"pull-request","url":"https://evil.example.com/p"}]}`,
			outcome: ResultInvalid,
			want:    &conveyorv1alpha1.TaskResult{Summary: "x"},
			dropped: 1,
		},
		{
			name:    "only rejected links leaves no Result",
			msg:     `{"links":[{"name":"pull-request","url":"https://evil.example.com/p"}]}`,
			outcome: ResultInvalid,
			dropped: 1,
		},
		{
			name: "http, user info, bad name and duplicate are dropped",
			msg: `{"summary":"x","links":[` +
				`{"name":"a","url":"http://github.com/x"},` +
				`{"name":"b","url":"https://user:pw@github.com/x"},` +
				`{"name":"Pull_Request","url":"https://github.com/x"},` +
				`{"name":"ok","url":"https://github.com/x"},` +
				`{"name":"ok","url":"https://github.com/y"}]}`,
			outcome: ResultInvalid,
			want: &conveyorv1alpha1.TaskResult{Summary: "x", Links: []conveyorv1alpha1.ResultLink{
				{Name: "ok", URL: "https://github.com/x"},
			}},
			dropped: 4,
		},
		{
			name:    "host match ignores case and the trailing dot",
			msg:     `{"links":[{"name":"doc","url":"https://example.org/a"}]}`,
			outcome: ResultRecorded,
			want: &conveyorv1alpha1.TaskResult{Links: []conveyorv1alpha1.ResultLink{
				{Name: "doc", URL: "https://example.org/a"},
			}},
		},
		{
			name: "IP hosts match IP and CIDR entries",
			msg: `{"links":[{"name":"v4","url":"https://203.0.113.9/a"},{"name":"v6","url":"https://[2001:db8::1]/b"},` +
				`{"name":"out","url":"https://198.51.100.1/c"}]}`,
			outcome: ResultInvalid,
			want: &conveyorv1alpha1.TaskResult{Links: []conveyorv1alpha1.ResultLink{
				{Name: "v4", URL: "https://203.0.113.9/a"},
				{Name: "v6", URL: "https://[2001:db8::1]/b"},
			}},
			dropped: 1,
		},
		{
			name:    "subdomain of an allowed name does not match",
			msg:     `{"links":[{"name":"x","url":"https://gist.github.com/a"}]}`,
			outcome: ResultInvalid,
			dropped: 1,
		},
		{
			name:    "control and format characters are removed",
			msg:     "{\"summary\":\"line one\\nline\\u0007 two\\u202e\"}",
			outcome: ResultRecorded,
			want:    &conveyorv1alpha1.TaskResult{Summary: "line one line two"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseResult(tt.msg, allow)
			if got.Outcome != tt.outcome {
				t.Fatalf("outcome: got %q (%s), want %q", got.Outcome, got.Problem, tt.outcome)
			}
			if got.DroppedLinks != tt.dropped {
				t.Errorf("dropped: got %d, want %d", got.DroppedLinks, tt.dropped)
			}
			if tt.outcome != ResultRecorded && got.Problem == "" {
				t.Errorf("expected a problem sentence for outcome %q", got.Outcome)
			}
			if fmt.Sprint(got.Result) != fmt.Sprint(tt.want) {
				t.Errorf("result: got %+v, want %+v", got.Result, tt.want)
			}
		})
	}
}

func TestParseResultTruncatesTheSummary(t *testing.T) {
	long := strings.Repeat("é", MaxResultSummaryChars+50)
	got := ParseResult(`{"summary":"`+long+`"}`, nil)
	if got.Outcome != ResultRecorded {
		t.Fatalf("outcome: got %q (%s)", got.Outcome, got.Problem)
	}
	if n := utf8.RuneCountInString(got.Result.Summary); n != MaxResultSummaryChars {
		t.Errorf("summary length: got %d characters, want %d", n, MaxResultSummaryChars)
	}
}

func TestParseResultKeepsAtMostEightLinks(t *testing.T) {
	links := make([]string, 0, MaxResultLinks+2)
	for i := range MaxResultLinks + 2 {
		links = append(links, fmt.Sprintf(`{"name":"l%d","url":"https://github.com/%d"}`, i, i))
	}
	got := ParseResult(`{"links":[`+strings.Join(links, ",")+`]}`, []string{"github.com"})
	if got.Outcome != ResultInvalid {
		t.Fatalf("outcome: got %q", got.Outcome)
	}
	if len(got.Result.Links) != MaxResultLinks || got.DroppedLinks != 2 {
		t.Errorf("links: got %d kept and %d dropped", len(got.Result.Links), got.DroppedLinks)
	}
}

func TestParseResultReadsOnlyTheKubeletLimit(t *testing.T) {
	// The JSON object starts after the 4096-byte limit, so the parser
	// never sees it.
	msg := strings.Repeat(" ", maxTerminationMessageBytes) + `{"summary":"late"}`
	if got := ParseResult(msg, nil); got.Outcome != ResultNotProvided {
		t.Errorf("outcome: got %q, want %q", got.Outcome, ResultNotProvided)
	}
}

func TestParseResultProblemNeverEchoesTheMessage(t *testing.T) {
	secret := "ghp_SECRETVALUE"
	for _, msg := range []string{
		secret,
		`{"summary":"` + secret,
		`{"` + secret + `":1}`,
		`{"links":[{"name":"x","url":"https://` + secret + `.example.com"}]}`,
	} {
		if got := ParseResult(msg, nil); strings.Contains(got.Problem, secret) {
			t.Errorf("problem %q echoes the message", got.Problem)
		}
	}
}
