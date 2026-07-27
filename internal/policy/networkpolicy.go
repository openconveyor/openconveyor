/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// Resolver abstracts DNS lookup so tests can inject fixed results.
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// lookupTimeout bounds a single hostname resolution. Lookups run
// sequentially inside a reconcile, so without a cap one hung resolver
// stalls a controller worker for the whole reconcile deadline.
const lookupTimeout = 5 * time.Second

// DefaultResolver uses the process's default net.Resolver.
type DefaultResolver struct{}

var _ Resolver = DefaultResolver{}

func (DefaultResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return net.DefaultResolver.LookupHost(ctx, host)
}

// StaticResolver serves fixed lookup results — the injectable test
// double for Resolver, shared by this package's tests and the
// controller's envtest suite. Err, when set, fails every lookup.
type StaticResolver struct {
	Hosts map[string][]string
	Err   error
}

var _ Resolver = StaticResolver{}

func (s StaticResolver) LookupHost(_ context.Context, host string) ([]string, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	return s.Hosts[host], nil
}

// DNS egress target: the stock CoreDNS label set on every mainstream
// k8s distro, reached on the standard DNS port.
const (
	dnsPort              = 53
	kubeSystemNamespace  = "kube-system"
	namespaceNameLabel   = "kubernetes.io/metadata.name"
	kubeDNSSelectorKey   = "k8s-app"
	kubeDNSSelectorValue = "kube-dns"
)

// BuildNetworkPolicy produces a NetworkPolicy that applies default-deny
// to the Task's pod. The policy is always emitted (never nil) so every
// Task gets the full default-deny baseline; callers never have to worry
// about a "no egress" edge case silently leaving the CNI wide open.
//
// When spec.permissions.egress is empty the result is pure deny-all:
// the pod cannot reach anything, not even DNS. This matches the intent
// that an agent with no declared outbound needs should not have any.
//
// When spec.permissions.egress is populated the policy also allows:
//
//   - DNS (53/udp + tcp) to kube-system/k8s-app=kube-dns — needed for
//     hostname entries to be useful from inside the pod.
//   - Each allowlisted egress target on all TCP and UDP ports.
//
// Hostnames are resolved once at reconcile time. DNS-based allowlists
// are a best-effort snapshot; for stable FQDN enforcement, users should
// deploy Cilium and let its FQDN policies do the work. Documented in
// docs/security-model.md.
func BuildNetworkPolicy(
	ctx context.Context,
	resolver Resolver,
	taskName, namespace string,
	egress []string,
) (*networkingv1.NetworkPolicy, error) {
	cidrs, err := resolveEgress(ctx, resolver, egress)
	if err != nil {
		return nil, err
	}

	var rules []networkingv1.NetworkPolicyEgressRule
	if len(egress) > 0 {
		rules = append(rules, dnsEgressRule())
		if len(cidrs) > 0 {
			rules = append(rules, networkingv1.NetworkPolicyEgressRule{
				To: toIPBlocks(cidrs),
			})
		}
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: ownedMeta(taskName, namespace, taskName),
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: PodSelectorLabels(taskName)},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Empty Ingress with PolicyTypes=[Ingress] means deny-all ingress
			// — agent Jobs have no reason to accept inbound connections.
			Ingress: nil,
			Egress:  rules,
		},
	}, nil
}

// resolveEgress normalises each entry in the allowlist to a CIDR string.
//
//   - Exact CIDR (contains "/") is passed through after parse validation.
//   - Hostname is resolved via the Resolver; each A/AAAA becomes /32 or /128.
//   - Bare IP is converted to /32 or /128.
//
// Duplicates are collapsed and the result is sorted for deterministic output.
func resolveEgress(ctx context.Context, resolver Resolver, egress []string) ([]string, error) {
	set := map[string]struct{}{}
	for _, entry := range egress {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return nil, &PermanentError{Err: fmt.Errorf("egress entry %q: invalid CIDR: %w", entry, err)}
			}
			set[entry] = struct{}{}
			continue
		}

		if ip := net.ParseIP(entry); ip != nil {
			set[ipToCIDR(ip)] = struct{}{}
			continue
		}

		addrs, err := lookupWithTimeout(ctx, resolver, entry)
		if err != nil {
			wrapped := fmt.Errorf("egress entry %q: lookup failed: %w", entry, err)
			var dnsErr *net.DNSError
			if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
				return nil, &PermanentError{Err: wrapped}
			}
			return nil, wrapped
		}
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil {
				set[ipToCIDR(ip)] = struct{}{}
			}
		}
	}

	return slices.Sorted(maps.Keys(set)), nil
}

// lookupWithTimeout resolves one host under lookupTimeout. Timeouts are
// transient by design (no PermanentError) so callers back off and retry.
func lookupWithTimeout(ctx context.Context, resolver Resolver, host string) ([]string, error) {
	lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	return resolver.LookupHost(lctx, host)
}

func ipToCIDR(ip net.IP) string {
	if ip.To4() != nil {
		return ip.String() + "/32"
	}
	return ip.String() + "/128"
}

func toIPBlocks(cidrs []string) []networkingv1.NetworkPolicyPeer {
	peers := make([]networkingv1.NetworkPolicyPeer, 0, len(cidrs))
	for _, c := range cidrs {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: c},
		})
	}
	return peers
}

// dnsEgressRule allows DNS queries to any pod labeled k8s-app=kube-dns in
// kube-system — the stock CoreDNS label set on every mainstream k8s distro.
// Without this rule the pod cannot resolve the names the user listed.
func dnsEgressRule() networkingv1.NetworkPolicyEgressRule {
	port := ptr.To(intstr.FromInt32(dnsPort))
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{
			{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{namespaceNameLabel: kubeSystemNamespace},
				},
				PodSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{kubeDNSSelectorKey: kubeDNSSelectorValue},
				},
			},
		},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: ptr.To(corev1.ProtocolUDP), Port: port},
			{Protocol: ptr.To(corev1.ProtocolTCP), Port: port},
		},
	}
}
