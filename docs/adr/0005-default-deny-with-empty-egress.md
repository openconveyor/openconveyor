# ADR-0005: Empty `permissions.egress` means deny-all — no implicit DNS

- **Status:** Accepted
- **Date:** 2026-04-18
- **Deciders:** Martijn Scholten

## Context

During Phase 2 we had to decide the shape of the NetworkPolicy when a
Task declares zero egress targets. There are two defensible shapes:

1. **Deny-all.** No egress rules. The pod cannot reach anything, not
   DNS and not in-cluster services.
2. **DNS-only.** A single egress rule that allows 53/udp+tcp to
   `kube-system/kube-dns`, so the pod can resolve names even if it has
   nothing explicit to contact.

Option 2 feels friendlier: the pod can at least run `getent hosts
foo.bar`. But it weakens the "default deny" contract. A Task with no
declared egress can still exfiltrate low-bandwidth data with DNS
queries to a resolver that the attacker controls. It can also
enumerate in-cluster services by name.

## Decision

For a Task with `spec.permissions.egress == []`, the controller creates
a NetworkPolicy with `PolicyTypes: [Ingress, Egress]` and **zero egress
rules**. The pod cannot reach anything off-box, not even DNS.

When `permissions.egress` is non-empty, the policy adds DNS to kube-dns
plus the resolved CIDRs. Without DNS, the hostname entries in the
allowlist would be useless. DNS is a consequence of declared network
access, not a default.

## Alternatives considered

- **Always allow DNS.** Rejected. The baseline is then not honest: an
  agent with "zero egress declared" can still do DNS-based
  exfiltration to `attacker.com` through any resolver. The selling
  point of OpenConveyor is a security baseline that matches the
  documentation; this alternative breaks that.
- **Allow DNS only to cluster DNS** (`kube-system/kube-dns`) by
  default, and block external resolvers. Rejected as a half-measure.
  It still lets a Task enumerate every Service in the cluster by name,
  which is reconnaissance-as-a-service.
- **Emit no NetworkPolicy at all when egress is empty.** Strongly
  rejected. On most CNIs that means egress that is not restricted
  (default allow). This would be a trap: operators read the code,
  assume "empty = locked down," and ship exposed pods.

## Consequences

- **Egress-empty Tasks can do nothing on the network.** If a Task
  needs DNS or in-cluster access, it has to say so. This pushes intent
  into the spec.
- **Declaring `permissions.egress: [cluster-internal-service]` works
  as expected.** The single declaration both allowlists the service
  and adds DNS. You do not need to list `kube-dns`.
- **Misconfiguration is loud.** A user who forgot to declare egress
  gets an agent that cannot even resolve `github.com`, and sees it
  immediately in the logs. This is the correct failure mode: louder is
  safer.

## References

- `internal/policy/networkpolicy.go:BuildNetworkPolicy`
- `internal/policy/networkpolicy_test.go` (the
  "zero egress → deny-all" case)
- `docs/security-model.md` § "Default-deny egress"
