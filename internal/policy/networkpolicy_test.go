/*
Copyright 2026.
*/

package policy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

func TestBuildNetworkPolicy(t *testing.T) {
	resolver := StaticResolver{
		Hosts: map[string][]string{
			"api.example.com": {"203.0.113.1", "2001:db8::1"},
			"only-v6.test":    {"2001:db8::42"},
		},
	}

	tests := []struct {
		name        string
		egress      []string
		wantEgress  int
		wantPeers   []string
		wantDNSRule bool
	}{
		{
			name:        "zero egress → deny-all (no egress rules)",
			egress:      nil,
			wantEgress:  0,
			wantPeers:   nil,
			wantDNSRule: false,
		},
		{
			name:        "cidr passthrough",
			egress:      []string{"10.0.0.0/8"},
			wantEgress:  2,
			wantPeers:   []string{"10.0.0.0/8"},
			wantDNSRule: true,
		},
		{
			name:        "ipv4 bare → /32",
			egress:      []string{"203.0.113.5"},
			wantEgress:  2,
			wantPeers:   []string{"203.0.113.5/32"},
			wantDNSRule: true,
		},
		{
			name:        "ipv6 bare → /128",
			egress:      []string{"2001:db8::5"},
			wantEgress:  2,
			wantPeers:   []string{"2001:db8::5/128"},
			wantDNSRule: true,
		},
		{
			name:        "hostname → resolved /32 + /128",
			egress:      []string{"api.example.com"},
			wantEgress:  2,
			wantPeers:   []string{"2001:db8::1/128", "203.0.113.1/32"},
			wantDNSRule: true,
		},
		{
			name:        "duplicates collapse",
			egress:      []string{"203.0.113.5", "203.0.113.5/32"},
			wantEgress:  2,
			wantPeers:   []string{"203.0.113.5/32"},
			wantDNSRule: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			np, err := BuildNetworkPolicy(context.Background(), resolver, "demo", "ns", tc.egress)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if np == nil {
				t.Fatal("policy should never be nil")
			}
			if got := len(np.Spec.Egress); got != tc.wantEgress {
				t.Fatalf("egress rule count: got %d want %d", got, tc.wantEgress)
			}
			if !hasIngressDenyAll(np) {
				t.Error("policy must apply deny-all ingress")
			}
			if tc.wantDNSRule && !hasDNSRule(np) {
				t.Error("expected DNS rule but not found")
			}
			if !tc.wantDNSRule && hasDNSRule(np) {
				t.Error("DNS rule present but egress was empty")
			}

			got := collectPeers(np)
			if !equalUnordered(got, tc.wantPeers) {
				t.Errorf("peers: got %v want %v", got, tc.wantPeers)
			}
		})
	}
}

func TestBuildNetworkPolicy_InvalidCIDR(t *testing.T) {
	_, err := BuildNetworkPolicy(context.Background(), StaticResolver{}, "demo", "ns", []string{"not-a-cidr/99"})
	if err == nil {
		t.Fatal("expected error on invalid CIDR")
	}
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Errorf("invalid CIDR must be a PermanentError, got %T: %v", err, err)
	}
}

func TestBuildNetworkPolicy_ResolverError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantPermanent bool
	}{
		{
			name:          "NXDOMAIN → permanent",
			err:           &net.DNSError{Err: "no such host", Name: "gone.example.com", IsNotFound: true},
			wantPermanent: true,
		},
		{
			name:          "DNS timeout → transient",
			err:           &net.DNSError{Err: "i/o timeout", Name: "api.example.com", IsTimeout: true, IsTemporary: true},
			wantPermanent: false,
		},
		{
			name:          "SERVFAIL → transient",
			err:           &net.DNSError{Err: "server misbehaving", Name: "api.example.com", IsTemporary: true},
			wantPermanent: false,
		},
		{
			name:          "unknown resolver error → transient",
			err:           fmt.Errorf("dns down"),
			wantPermanent: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolver := StaticResolver{Err: tc.err}
			_, err := BuildNetworkPolicy(context.Background(), resolver, "demo", "ns", []string{"api.example.com"})
			if err == nil {
				t.Fatal("expected error when resolver fails")
			}
			var perm *PermanentError
			if got := errors.As(err, &perm); got != tc.wantPermanent {
				t.Errorf("PermanentError: got %v want %v (err: %v)", got, tc.wantPermanent, err)
			}
			if !errors.Is(err, tc.err) {
				t.Errorf("original error must stay unwrappable, got %v", err)
			}
		})
	}
}

// deadlineCheckingResolver records whether the ctx it receives carries a
// deadline, and honors cancellation so a hung lookup cannot outlive it.
type deadlineCheckingResolver struct {
	sawDeadline *bool
}

func (r deadlineCheckingResolver) LookupHost(ctx context.Context, _ string) ([]string, error) {
	_, ok := ctx.Deadline()
	*r.sawDeadline = ok
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestBuildNetworkPolicy_LookupIsBounded(t *testing.T) {
	var sawDeadline bool
	// Parent ctx is cancelled immediately so the blocking resolver returns
	// right away — the assertion is about the per-lookup deadline existing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := BuildNetworkPolicy(ctx, deadlineCheckingResolver{sawDeadline: &sawDeadline}, "demo", "ns", []string{"api.example.com"})
	if err == nil {
		t.Fatal("expected error from cancelled lookup")
	}
	if !sawDeadline {
		t.Error("resolver ctx has no deadline — a hung resolver would stall the reconcile worker")
	}
	var perm *PermanentError
	if errors.As(err, &perm) {
		t.Errorf("timeout must stay transient, got PermanentError: %v", err)
	}
}

func TestBuildNetworkPolicy_PodSelectorBindsToTask(t *testing.T) {
	np, err := BuildNetworkPolicy(context.Background(), StaticResolver{}, "demo-task", "ns", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := np.Spec.PodSelector.MatchLabels[LabelTaskName]
	if got != "demo-task" {
		t.Errorf("podSelector.matchLabels[%s]: got %q want %q", LabelTaskName, got, "demo-task")
	}
}

func hasIngressDenyAll(np *networkingv1.NetworkPolicy) bool {
	if np.Spec.Ingress != nil {
		return false
	}
	return slices.Contains(np.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
}

func hasDNSRule(np *networkingv1.NetworkPolicy) bool {
	for _, rule := range np.Spec.Egress {
		for _, port := range rule.Ports {
			if port.Port == nil || port.Port.IntValue() != 53 {
				continue
			}
			for _, peer := range rule.To {
				if peer.PodSelector != nil && peer.PodSelector.MatchLabels["k8s-app"] == "kube-dns" {
					return true
				}
			}
		}
	}
	return false
}

func collectPeers(np *networkingv1.NetworkPolicy) []string {
	var out []string
	for _, rule := range np.Spec.Egress {
		// Skip DNS rule — we assert that separately.
		isDNS := false
		for _, p := range rule.Ports {
			if p.Port != nil && p.Port.IntValue() == 53 && p.Protocol != nil &&
				(*p.Protocol == corev1.ProtocolTCP || *p.Protocol == corev1.ProtocolUDP) {
				isDNS = true
				break
			}
		}
		if isDNS {
			continue
		}
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				out = append(out, peer.IPBlock.CIDR)
			}
		}
	}
	return out
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}
