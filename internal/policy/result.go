/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	conveyorv1alpha1 "github.com/openconveyor/openconveyor/api/v1alpha1"
)

// The limits of the Result contract (ADR-0008). The agent writes at most
// MaxResultBytes. The kubelet keeps at most maxTerminationMessageBytes of the
// file, so the parser never reads more than that.
const (
	MaxResultBytes             = 2048
	MaxResultSummaryChars      = 512
	MaxResultLinks             = 8
	MaxResultLinkNameChars     = 32
	MaxResultLinkURLChars      = 2048
	maxTerminationMessageBytes = 4096
)

// ResultOutcome tells what ParseResult found in a termination message.
type ResultOutcome string

const (
	// ResultRecorded means that the message held a valid Result and the
	// parser kept all of it.
	ResultRecorded ResultOutcome = "Recorded"
	// ResultNotProvided means that the message held no Result: it was
	// empty, it held no JSON object, or the object was empty.
	ResultNotProvided ResultOutcome = "NotProvided"
	// ResultInvalid means that the message held a JSON object that broke
	// the contract, or that the parser dropped one or more links.
	ResultInvalid ResultOutcome = "Invalid"
)

// ParsedResult is the output of ParseResult.
//
// Problem is a fixed sentence that names the first rule that the message
// broke. It never contains text from the message, so the caller can put it
// in a condition, an Event or a log line.
type ParsedResult struct {
	Result       *conveyorv1alpha1.TaskResult
	Outcome      ResultOutcome
	DroppedLinks int
	Problem      string
}

// wireResult is the strict file shape. Unknown fields are an error.
type wireResult struct {
	Summary string     `json:"summary"`
	Links   []wireLink `json:"links"`
}

type wireLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

var linkNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ParseResult validates a container termination message as a Result.
//
// Treat the message as untrusted input: the agent, and so a hostile
// prompt, controls it. The parser reads at most 4096 bytes, finds the first "{",
// and decodes strict JSON with unknown fields rejected. The object must
// be at most MaxResultBytes. The parser removes control characters from
// the summary and cuts it to MaxResultSummaryChars. It keeps a link only
// when the name is kebab-case, the URL uses https without user info, and
// the host is in allowlist. The allowlist is the effective egress
// allowlist of the Task: DNS names, IP addresses and CIDR blocks.
func ParseResult(message string, allowlist []string) ParsedResult {
	if len(message) > maxTerminationMessageBytes {
		message = message[:maxTerminationMessageBytes]
	}
	start := strings.IndexByte(message, '{')
	if start < 0 {
		problem := "Termination message is empty"
		if strings.TrimSpace(message) != "" {
			problem = "Termination message holds no JSON object"
		}
		return ParsedResult{Outcome: ResultNotProvided, Problem: problem}
	}
	body := message[start:]
	if len(body) > MaxResultBytes {
		return ParsedResult{
			Outcome: ResultInvalid,
			Problem: fmt.Sprintf("Result is larger than %d bytes", MaxResultBytes),
		}
	}

	var wire wireResult
	if err := decodeStrict([]byte(body), &wire); err != nil {
		return ParsedResult{Outcome: ResultInvalid, Problem: "Result is not strict JSON of the contract shape"}
	}

	out := &conveyorv1alpha1.TaskResult{Summary: sanitizeSummary(wire.Summary)}
	dropped := 0
	seen := map[string]bool{}
	for _, l := range wire.Links {
		link, ok := validateLink(l, allowlist)
		if !ok || seen[link.Name] || len(out.Links) == MaxResultLinks {
			dropped++
			continue
		}
		seen[link.Name] = true
		out.Links = append(out.Links, link)
	}

	empty := out.Summary == "" && len(out.Links) == 0
	switch {
	case dropped > 0 && empty:
		return ParsedResult{Outcome: ResultInvalid, DroppedLinks: dropped, Problem: droppedProblem(dropped, len(wire.Links))}
	case dropped > 0:
		return ParsedResult{Result: out, Outcome: ResultInvalid, DroppedLinks: dropped,
			Problem: droppedProblem(dropped, len(wire.Links))}
	case empty:
		return ParsedResult{Outcome: ResultNotProvided, Problem: "Result is empty"}
	default:
		return ParsedResult{Result: out, Outcome: ResultRecorded}
	}
}

func droppedProblem(dropped, total int) string {
	return fmt.Sprintf("Dropped %d of %d links that broke the contract", dropped, total)
}

// decodeStrict decodes exactly one JSON value into v. It rejects unknown
// fields and any data after the value except white space.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the JSON object")
	}
	return nil
}

// sanitizeSummary removes invalid UTF-8, control characters and format
// characters (for example bidirectional overrides). It replaces tabs and
// line breaks with a space, and cuts the text to MaxResultSummaryChars.
func sanitizeSummary(s string) string {
	s = strings.ToValidUTF8(s, "")
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == MaxResultSummaryChars {
			break
		}
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			r = ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			continue
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func validateLink(l wireLink, allowlist []string) (conveyorv1alpha1.ResultLink, bool) {
	if len(l.Name) > MaxResultLinkNameChars || !linkNamePattern.MatchString(l.Name) {
		return conveyorv1alpha1.ResultLink{}, false
	}
	if l.URL == "" || len(l.URL) > MaxResultLinkURLChars {
		return conveyorv1alpha1.ResultLink{}, false
	}
	u, err := url.Parse(l.URL)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return conveyorv1alpha1.ResultLink{}, false
	}
	if !hostAllowed(u.Hostname(), allowlist) {
		return conveyorv1alpha1.ResultLink{}, false
	}
	normalized := u.String()
	if len(normalized) > MaxResultLinkURLChars {
		return conveyorv1alpha1.ResultLink{}, false
	}
	return conveyorv1alpha1.ResultLink{Name: l.Name, URL: normalized}, true
}

// hostAllowed reports whether host is in the egress allowlist. A DNS name
// matches an entry with the same name, without regard to case or a
// trailing dot. An IP address matches an equal IP entry or a CIDR entry
// that contains it. There are no wildcards.
func hostAllowed(host string, allowlist []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	ip := net.ParseIP(host)
	for _, entry := range allowlist {
		entry = strings.TrimSpace(entry)
		if ip != nil {
			if _, cidr, err := net.ParseCIDR(entry); err == nil && cidr.Contains(ip) {
				return true
			}
			if eip := net.ParseIP(entry); eip != nil && eip.Equal(ip) {
				return true
			}
			continue
		}
		if strings.TrimSuffix(strings.ToLower(entry), ".") == host {
			return true
		}
	}
	return false
}
