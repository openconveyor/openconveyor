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

// StaticResolver serves fixed lookup results. It is the injectable test
// double for Resolver. The tests of this package and the envtest suite of
// the controller share it. When a test sets Err, every lookup fails.
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

// DefaultEgressPort is the only destination port the egress allowlist
// opens when spec.permissions.egressPorts is unset. HTTPS covers every
// API that an agent normally calls. The Task must declare any other port.
const DefaultEgressPort int32 = 443

// The DNS egress target is the stock CoreDNS label set on every
// mainstream Kubernetes distribution, on the standard DNS port.
const (
	dnsPort              = 53
	kubeSystemNamespace  = "kube-system"
	namespaceNameLabel   = "kubernetes.io/metadata.name"
	kubeDNSSelectorKey   = "k8s-app"
	kubeDNSSelectorValue = "kube-dns"
)

// BuildNetworkPolicy produces a NetworkPolicy that applies default-deny
// to the pod of the Task. The function always returns a policy, never nil,
// so every Task has the full default-deny baseline. Callers do not have
// to handle a "no egress" edge case that leaves the CNI not restricted.
//
// When spec.permissions.egress is empty, the result is pure deny-all: the
// pod cannot reach anything, not even DNS. This matches the intent that an
// agent with no declared outbound needs has no outbound access.
//
// When spec.permissions.egress is not empty, the policy also allows:
//
//   - DNS (53/udp and tcp) to kube-system/k8s-app=kube-dns. The pod needs
//     this rule to use the hostname entries.
//   - Each allowlisted egress target on the given ports (TCP and UDP), or
//     on DefaultEgressPort alone when ports is empty.
//
// The function resolves hostnames once at reconcile time. A DNS-based
// allowlist is a best-effort snapshot. For stable FQDN enforcement, deploy
// Cilium and let its FQDN policies do the work. docs/security-model.md
// documents this.
func BuildNetworkPolicy(
	ctx context.Context,
	resolver Resolver,
	taskName, namespace string,
	egress []string,
	ports []int32,
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
				To:    toIPBlocks(cidrs),
				Ports: egressPorts(ports),
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
			// An empty Ingress with PolicyTypes=[Ingress] means deny-all ingress.
			// Agent Jobs have no reason to accept inbound connections.
			Ingress: nil,
			Egress:  rules,
		},
	}, nil
}

// HasHostnames reports whether any allowlist entry is a DNS name and not
// an IP or CIDR. Only names drift, so the controller re-resolves the
// NetworkPolicy only for Tasks with names while they run.
func HasHostnames(egress []string) bool {
	for _, entry := range egress {
		entry = strings.TrimSpace(entry)
		if entry == "" || strings.Contains(entry, "/") || net.ParseIP(entry) != nil {
			continue
		}
		return true
	}
	return false
}

// resolveEgress normalizes each entry in the allowlist to a CIDR string.
//
//   - The function validates an exact CIDR (contains "/") and passes it through.
//   - The function resolves a hostname with the Resolver; each A/AAAA record
//     becomes /32 or /128.
//   - The function converts a bare IP to /32 or /128.
//
// The function collapses duplicates and sorts the result for deterministic output.
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

// lookupWithTimeout resolves one host under lookupTimeout. By design, a
// timeout is transient and not a PermanentError, so callers back off and
// retry.
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

// egressPorts expands the declared ports, or DefaultEgressPort when there
// are none, into one TCP and one UDP entry each. It sorts and deduplicates
// the ports. It opens both protocols because the previous rule allowed
// both and HTTP/3 runs over UDP 443.
func egressPorts(ports []int32) []networkingv1.NetworkPolicyPort {
	if len(ports) == 0 {
		ports = []int32{DefaultEgressPort}
	}
	ports = slices.Sorted(slices.Values(ports))
	ports = slices.Compact(ports)
	out := make([]networkingv1.NetworkPolicyPort, 0, 2*len(ports))
	for _, p := range ports {
		port := new(intstr.FromInt32(p))
		out = append(out,
			networkingv1.NetworkPolicyPort{Protocol: ptr.To(corev1.ProtocolTCP), Port: port},
			networkingv1.NetworkPolicyPort{Protocol: ptr.To(corev1.ProtocolUDP), Port: port},
		)
	}
	return out
}

// dnsEgressRule allows DNS queries to any pod with the label
// k8s-app=kube-dns in kube-system. This is the stock CoreDNS label set on
// every mainstream Kubernetes distribution. Without this rule, the pod
// cannot resolve the names that the user listed.
func dnsEgressRule() networkingv1.NetworkPolicyEgressRule {
	port := new(intstr.FromInt32(dnsPort))
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
