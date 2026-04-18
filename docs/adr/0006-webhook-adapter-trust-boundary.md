# ADR-0006: Webhook adapter trust boundary — HMAC, capped body, namespace-local secrets

- **Status:** Accepted
- **Date:** 2026-04-18
- **Deciders:** Martijn Scholten

## Context

The reference webhook adapter (Phase 4) is an HTTP endpoint reachable
from the internet in a typical deployment (GitHub / Linear / GitLab
need to POST to it). It creates `Task` CRs — which, via the
reconciler, mint Jobs and Secret projections. A compromised trigger
is therefore a privilege escalation from "public HTTP" to "agent runs
on cluster with declared secrets."

Several questions needed decisions before the adapter shipped:

1. What authentication mechanism do we support? OAuth? mTLS? HMAC?
   All three?
2. Where do the HMAC signing keys live?
3. How much of the payload do we accept?
4. What happens when a `ClusterTriggerClass` has a mapping that fails
   to resolve?

## Decision

**1. HMAC-SHA256 only, shared secret per ClusterTriggerClass.** The
adapter validates `HMAC(secret, rawBody) == headerValue` in constant
time. No OAuth, no mTLS at the application layer. Users who need
stronger auth terminate it at their ingress / reverse proxy.

**2. Signing secrets live in the adapter's own namespace.** The
`ClusterTriggerClass.spec.signature.secretRef` names a Secret by name
+ key; the adapter resolves it against `--trigger-namespace` (default
`conveyor-system`). Secrets never cross namespaces.

**3. Bodies are capped at 1 MiB.** Enforced via
`http.MaxBytesReader`. Anything larger gets a 413.

**4. Missing mapping resolutions fall back to `FieldMapping.Default`
or are dropped silently.** A Task that ends up without a prompt
returns 400; we do not create a prompt-less Task and let the
reconciler complain.

## Alternatives considered

- **Allow OAuth / mTLS in-process.** Rejected for v0.1. HTTPS + HMAC
  covers Linear, GitHub, GitLab, Forgejo — the v1 target. mTLS is
  better handled by the cluster's ingress; bolting it into the
  adapter would duplicate machinery operators already deploy.
- **Secrets in the Task's namespace instead of the adapter's.**
  Rejected. The adapter does not know which namespace the Task will
  land in *until* mappings are applied — checking the signature after
  the mappings run creates a time-of-check/time-of-use gap.
- **Unbounded bodies.** Rejected. A misbehaving source (or a forged
  one) can exhaust memory in a request loop. 1 MiB is ~100× the
  largest real webhook payload we have seen in practice.
- **Fail loud on any unresolved mapping.** Rejected as a default —
  webhook payloads vary by action type (an "issue" event has no
  `pull_request`, and vice-versa). Dropping unresolvable mappings
  makes a single `ClusterTriggerClass` usable across actions.

## Consequences

- **Rotating a webhook secret requires editing the referenced Secret
  only.** No OpenConveyor restart, no CRD edit. The next request picks
  up the new value.
- **A ClusterTriggerClass without filters is dangerous.** Any valid
  signature produces a Task. Document `filters: [...]` as a de-facto
  requirement in the plugin-authoring guide; consider a CEL-based
  admission check later.
- **HMAC is only as strong as the shared secret.** Document the
  minimum length (>= 32 bytes of entropy) and rotation cadence in the
  security model.
- **The adapter cannot distinguish a well-formed "wrong-event" webhook
  from malicious traffic.** A GitHub "issue closed" with a valid
  signature gets a 202 (accepted, filtered). This is intentional —
  the attacker already proved they have the secret, so signature +
  filter together are the gate.

## References

- `internal/trigger/handler.go`
- `internal/trigger/signature.go`
- `internal/trigger/mapping.go`
- `api/v1alpha1/clustertriggerclass_types.go`
- `config/samples/conveyor_v1alpha1_clustertriggerclass.yaml`
