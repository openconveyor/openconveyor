# ADR-0004: Project secrets as files, never as env vars

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

Kubernetes offers two mainstream ways to expose Secret data to a pod:
`envFrom`/`env.valueFrom.secretKeyRef`, and volume projection. Most
examples on the internet use env vars because they are two lines of
YAML. The value proposition of OpenConveyor is "least-privilege agent
runs," so the choice of mechanism is a security decision.

## Decision

OpenConveyor always projects the secrets in
`Task.spec.permissions.secrets` as read-only volumes at
`/run/secrets/<name>/`. OpenConveyor never writes a Secret to an env
var. A user who wants a secret in an env var can source it inside the
container at runtime. That is their choice, not ours.

## Alternatives considered

- **Env vars with `envFrom`.** Rejected. Env vars are in
  `/proc/<pid>/environ`, and every child process that the agent
  creates inherits them. A sub-process that crashes and dumps its
  environment to stderr, or a diagnostic tool that prints
  `/proc/self/environ`, leaks the secret. File-based secrets have
  POSIX permissions and do not travel with `exec`.
- **Both, configurable per secret.** Rejected. "Configurable security
  defaults" is another way to say "someone selects the insecure
  default." The ceremony to go from a file to an env var is small; the
  cost of a leaked token is large.
- **Project everything into one directory** (`/etc/openconveyor/secrets/...`).
  Rejected in favor of `/run/secrets/<name>/`. `/run` is the
  conventional tmpfs mount point; `/etc/openconveyor` would imply
  persistence.

## Consequences

- **Agent image authors have to write agents that read files.** An
  agent that assumes `ANTHROPIC_API_KEY=$(env)` does not work as-is.
  The reference agent images (for example `claude-code-implementer`)
  read the file and export the env var internally before they invoke
  the underlying CLI.
- **Listing `/run/secrets` is the inventory check.** An auditor or a
  nervous operator can run `kubectl exec -- ls /run/secrets` to see
  exactly which secrets this Task can access. That matches the
  declarative model: the Task spec and the disk must agree.
- **Secret rotation is eventual.** The kubelet updates a projected
  Secret volume when the underlying Secret changes, but with a
  kubelet-dependent delay (tens of seconds). Agent runs are one-shot
  and short; rotation during a run is not a concern in practice.

## References

- `internal/policy/secrets.go`
- `docs/security-model.md` § "Declared secrets only"
- Kubernetes docs: "Configure a Pod to Use a Projected Volume for
  Storage"
