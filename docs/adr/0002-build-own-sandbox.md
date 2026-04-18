# ADR-0002: Build our own sandbox primitive; do not adopt `kubernetes-sigs/agent-sandbox`

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

`kubernetes-sigs/agent-sandbox` (SIG Apps, sponsored by Google)
defines a CRD for long-running agent _workspaces_: gVisor/Kata
isolation, warm pools, pause/resume semantics. The target use case
is IDE-style sessions (think Replit) where the agent is interactive
and persists across requests.

OpenConveyor needs one-shot execution: a trigger fires, an agent runs,
a PR or a report comes out, the Job exits. We had to decide whether
to adopt agent-sandbox as the backend or roll our own.

## Decision

We use a plain Kubernetes `Job` with hardened `PodSpec` defaults
(Pod Security Standard _restricted_ plus a few extras) as the
execution primitive. We do not depend on agent-sandbox.

We keep the door open: the agent execution step is wrapped in
`internal/controller/build.go`, so a future alternative backend
(agent-sandbox, KubeVirt, whatever) can plug in without touching
the CRD surface or the policy generator.

## Alternatives considered

- **Adopt agent-sandbox as the backend.** Rejected. It is designed
  for long-running stateful workspaces; the mental model is "a
  sandbox that exists and you schedule work onto it." Our model is
  "a Job that runs once and dies." Forcing our shape into theirs
  means carrying warm-pool/pause/resume semantics we do not use,
  and inheriting their dependency surface (currently assumes gVisor
  or Kata nodes). Overkill for homelab k3s.
- **Use raw `Pod` + a watch loop** instead of `Job`. Rejected.
  `Job` already gives us backoff control, `activeDeadlineSeconds`,
  TTL-based cleanup, and status conditions that map cleanly to our
  phase model. Reinventing that is pure loss.
- **Write our own `AgentRun` CRD** that wraps a pod spec directly.
  Rejected. Same gains as raw Pod, same loss. Plus operators
  already know `kubectl logs job/<name>`; we should ride that
  familiarity.

## Consequences

- **Runs on any cluster with basic NetworkPolicy support.** No
  RuntimeClass requirement, no node-level gVisor setup. k3s works
  out of the box.
- **Weaker isolation than a real sandbox.** A kernel exploit or a
  containerd CVE defeats our model. For untrusted agent code,
  operators pair OpenConveyor with `RuntimeClass: gvisor` or `kata`
  at the pod spec level — that is an opt-in, not our baseline.
  Documented in `docs/security-model.md`.
- **Adopting agent-sandbox later is still possible.** The
  execution step is behind `buildJob`; a second backend can be
  introduced as a `spec.runtime` field that picks between "Job"
  and "Sandbox" without any schema change to the user-facing
  permission/resource blocks.

## References

- `internal/controller/build.go:buildJob`
- `docs/security-model.md`
- agent-sandbox project: github.com/kubernetes-sigs/agent-sandbox
