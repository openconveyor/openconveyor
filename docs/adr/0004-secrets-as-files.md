# ADR-0004: Project secrets as files, never as env vars

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

Kubernetes offers two mainstream ways to surface Secret data into a
pod: `envFrom`/`env.valueFrom.secretKeyRef`, and volume projection.
Most examples on the internet use env vars because they are two
lines of YAML. For an orchestrator whose value proposition is
"least-privilege agent runs," picking the right mechanism is a
security decision.

## Decision

Secrets declared in `Task.spec.permissions.secrets` are always
projected as read-only volumes at `/run/secrets/<name>/`. OpenConveyor
never writes a Secret to an env var. If a user wants a secret in
an env var they can source it inside the container at runtime —
that is their choice to make, not ours.

## Alternatives considered

- **Env vars via `envFrom`.** Rejected. Env vars live in
  `/proc/<pid>/environ` and are inherited by every child process the
  agent spawns. A sub-process that crashes and dumps its environment
  to stderr, or a diagnostic tool that prints `/proc/self/environ`,
  leaks the secret. File-based secrets have POSIX permissions and
  do not travel with `exec`.
- **Both, configurable per-secret.** Rejected. "Configurable
  security defaults" is another way to say "the insecure default
  will get picked." The ceremony to go from file → env var is
  small; the cost of a leaked token is large.
- **Project everything into one directory** (`/etc/openconveyor/secrets/...`).
  Rejected in favor of `/run/secrets/<name>/`. `/run` is the
  conventional tmpfs mount point; `/etc/openconveyor` would imply
  persistence.

## Consequences

- **Agent images have to be written to read files.** Agents that
  assume `ANTHROPIC_API_KEY=$(env)` will not work as-is. Reference
  agent images (`claude-code-implementer`, etc.) read the file and
  export the env var internally before invoking the underlying CLI.
- **Listing `/run/secrets` is the inventory check.** An auditor or
  a nervous operator can `kubectl exec -- ls /run/secrets` to see
  exactly what this Task has access to. That matches the
  declarative model: what is in the Task spec should match what is
  on disk.
- **Secret rotation is eventual.** Projected Secret volumes update
  when the underlying Secret changes, but with kubelet-dependent
  delay (tens of seconds). Agent runs are one-shot and short;
  rotation during a run is not a concern in practice.

## References

- `internal/policy/secrets.go`
- `docs/security-model.md` § "Declared secrets only"
- Kubernetes docs: "Configure a Pod to Use a Projected Volume for
  Storage"
