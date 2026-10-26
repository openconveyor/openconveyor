# ADR-0002: Build our own sandbox primitive; do not adopt `kubernetes-sigs/agent-sandbox`

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

`kubernetes-sigs/agent-sandbox` (SIG Apps, sponsored by Google)
defines a CRD for long-running agent _workspaces_: gVisor/Kata
isolation, warm pools, and pause/resume semantics. Its target use case
is an IDE-style session, for example Replit, where the agent is
interactive and persists across requests.

OpenConveyor needs one-shot execution: a trigger starts, an agent runs,
a PR or a report comes out, and the Job exits. We had to decide between
agent-sandbox as the backend and our own primitive.

## Decision

We use a plain Kubernetes `Job` with hardened `PodSpec` defaults
(Pod Security Standard _restricted_ plus a few extras) as the
execution primitive. We do not depend on agent-sandbox.

We keep the door open: `internal/controller/build.go` wraps the agent
execution step. A future alternative backend (agent-sandbox, KubeVirt,
or another) can plug in without a change to the CRD surface or the
policy generator.

## Alternatives considered

- **Adopt agent-sandbox as the backend.** Rejected. Its authors
  designed it for long-running stateful workspaces; its mental model
  is "a sandbox exists, and you schedule work onto it." Our model is
  "a Job runs once and exits." If we force our shape into theirs, we
  carry warm-pool and pause/resume semantics that we do not use. We
  also inherit their dependency surface, which currently assumes
  gVisor or Kata nodes. That is overkill for homelab k3s.
- **Use a raw `Pod` + a watch loop** instead of `Job`. Rejected.
  `Job` already gives us backoff control, `activeDeadlineSeconds`,
  TTL-based cleanup, and status conditions that map cleanly to our
  phase model. A new implementation of that is pure loss.
- **Write our own `AgentRun` CRD** that wraps a pod spec directly.
  Rejected. It has the same gains and the same loss as a raw Pod.
  Also, operators already know `kubectl logs job/<name>`; we keep
  that familiarity.

## Consequences

- **Runs on any cluster with basic NetworkPolicy support.** There is
  no RuntimeClass requirement and no node-level gVisor setup. k3s
  works out of the box.
- **Weaker isolation than a real sandbox.** A kernel exploit or a
  containerd CVE defeats our model. For untrusted agent code,
  operators pair OpenConveyor with `RuntimeClass: gvisor` or `kata`
  at the pod spec level. That is an opt-in, not our baseline.
  `docs/security-model.md` documents it.
- **Adopting agent-sandbox later is still possible.** The execution
  step is behind `buildJob`. We can introduce a second backend as a
  `spec.runtime` field that selects between "Job" and "Sandbox". That
  needs no schema change to the user-facing permission/resource
  blocks.

## References

- `internal/controller/build.go:buildJob`
- `docs/security-model.md`
- agent-sandbox project: github.com/kubernetes-sigs/agent-sandbox
