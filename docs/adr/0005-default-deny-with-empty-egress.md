# ADR-0005: Empty `permissions.egress` means deny-all — no implicit DNS

- **Status:** Accepted
- **Date:** 2026-04-18
- **Deciders:** Martijn Scholten

## Context

During Phase 2 we had to decide what the NetworkPolicy should look
like when a Task declares zero egress targets. Two defensible
shapes:

1. **Deny-all.** No egress rules. Pod cannot reach anything —
   including DNS, including in-cluster services.
2. **DNS-only.** A single egress rule allowing 53/udp+tcp to
   `kube-system/kube-dns`, so the pod can resolve names even if it
   has nothing explicit to contact.

Option 2 feels friendlier (the pod can at least `getent hosts
foo.bar`) but it weakens the "default deny" contract: a Task with
no declared egress can still exfiltrate low-bandwidth data via DNS
queries to an attacker-controlled resolver, or enumerate
in-cluster services by name.

## Decision

A Task with `spec.permissions.egress == []` gets a NetworkPolicy
with `PolicyTypes: [Ingress, Egress]` and **zero egress rules**.
The pod cannot reach anything off-box, not even DNS.

When `permissions.egress` is non-empty, the policy adds DNS to
kube-dns (because hostname entries in the allowlist would otherwise
be useless) plus the resolved CIDRs. DNS is a consequence of
declaring network access, not a default.

## Alternatives considered

- **Always allow DNS.** Rejected. Means the baseline is not
  honest: an agent with "zero egress declared" can still do
  DNS-based exfiltration to `attacker.com` via any resolver. The
  selling point of OpenConveyor is a security baseline that matches
  the documentation; this alternative breaks that.
- **Allow DNS only to cluster DNS** (`kube-system/kube-dns`) by
  default, block external resolvers. Rejected as half-measure.
  Still lets a Task enumerate every Service in the cluster by
  name, which is reconnaissance-as-a-service.
- **Emit no NetworkPolicy at all when egress is empty.** Strongly
  rejected. On most CNIs that means wide-open egress (default
  allow). This would be a trap: operators would read the code,
  assume "empty = locked down," and ship exposed pods.

## Consequences

- **Egress-empty Tasks literally can do nothing network-wise.** If
  a Task needs DNS or in-cluster access, it has to say so. This
  pushes intent into the spec.
- **Declaring `permissions.egress: [cluster-internal-service]`
  works as expected.** The single declaration both allowlists the
  service and pulls in DNS — no need to list `kube-dns` explicitly.
- **Misconfiguration is loud.** A user who forgot to declare egress
  gets an agent that cannot even resolve `github.com`; they see it
  immediately in logs. This is the correct failure mode — louder
  is safer.

## References

- `internal/policy/networkpolicy.go:BuildNetworkPolicy`
- `internal/policy/networkpolicy_test.go` (the
  "zero egress → deny-all" case)
- `docs/security-model.md` § "Default-deny egress"
