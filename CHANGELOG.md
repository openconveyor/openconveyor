# Changelog

All notable changes to OpenConveyor are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] — 2026-04-26

First public alpha release. Installable in one command:

```sh
kubectl apply -f https://github.com/openconveyor/openconveyor/releases/download/v0.1.0/install.yaml
```

Multi-arch images (`linux/amd64` + `linux/arm64`) for the controller
and the three reference agents are published to
`ghcr.io/openconveyor/{conveyor,agent-claude-implementer,agent-claude-reviewer,agent-claude-reporter}:v0.1.0`.

### Added

- **Task CRD** (`openconveyor.ai/v1alpha1`) — declares an agent
  invocation: agent reference, prompt source (inline /
  configMapRef / secretRef), declared secrets and egress allowlist,
  optional RBAC rules, CPU/memory/timeout.
- **ClusterAgentClass CRD** — cluster-wide agent definition:
  container image, command, input mapping, baseline permission
  requirements that are unioned with per-Task permissions.
- **ClusterTriggerClass CRD** — webhook trigger template: HMAC
  signature validation, AND-composed gjson filters, field mappings
  into a Task template.
- **Task controller** — reconciles a Task into an owned
  ServiceAccount, NetworkPolicy, Role/RoleBinding (only when RBAC is
  declared), projected Secret volumes, prompt ConfigMap (for inline
  prompts), and a hardened one-shot Job.
- **Security baseline** on every generated Job:
  `runAsNonRoot: true`, `runAsUser/Group: 65532`,
  `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`,
  all capabilities dropped, `seccompProfile: RuntimeDefault`,
  `restartPolicy: Never`, `backoffLimit: 0`, mandatory
  `activeDeadlineSeconds`, `automountServiceAccountToken: false`
  unless RBAC is declared.
- **Default-deny NetworkPolicy** with DNS egress to `kube-dns`
  only when the Task declares at least one egress target.
- **Webhook trigger adapter** — HTTP server, HMAC-SHA256 signature
  validation with constant-time compare, 1 MiB body cap, gjson-based
  filter evaluation and field mapping, `/healthz` endpoint.
- **Reference agent** (`agents/claude-code-implementer/`) — reads
  the prompt from a projected file, clones the target repo, runs
  Claude Code, opens a pull request.
- **Reviewer reference agent** (`agents/claude-code-reviewer/`) —
  extracts a PR URL from the prompt, fetches the diff with `gh pr
  diff`, runs Claude Code, posts the result via `gh pr review
  --comment`. No `git push` path, no PR-open path.
- **Reporter reference agent** (`agents/claude-code-reporter/`) —
  gathers data from the GitHub API, runs Claude Code to produce a
  summary, and posts the result as a GitHub Issue comment. No git
  clone, no git push — read-only agent.
- **CronJob reporter example** (`examples/cronjob-reporter/`) —
  scheduled Task dispatch via stock Kubernetes CronJob. Proves the
  out-of-process trigger pattern for recurring tasks (ADR-0003).
- **Phone workflow documentation** (`docs/phone-workflow.md`) —
  step-by-step guide for iterative development from a phone using
  GitHub Issues as the dispatch mechanism.
- **Five worked examples** under `examples/`: GitHub Issues
  webhook, Linear workflow-state webhook, GitHub Pull Requests
  webhook (reviewer), Claude Code `/conveyor` slash-command
  dispatcher, and CronJob reporter.
- **Architecture docs** (`docs/architecture.md`),
  **security model** (`docs/security-model.md`), and six
  Accepted ADRs (`docs/adr/0001`–`0006`).
- **Prometheus metrics** (`internal/metrics/`) exposed on the manager's
  existing `/metrics` endpoint:
  `conveyor_task_phase_transitions_total{phase,reason}`,
  `conveyor_task_reconcile_errors_total{step}`,
  `conveyor_task_duration_seconds{phase}` (histogram), and
  `conveyor_webhook_requests_total{trigger_class,result}`.
- **`status.observedGeneration`** on Task — the most recent
  `metadata.generation` the controller has reconciled against.
  Makes stale-status detection trivial for consumers.
- **Extra Task print columns** — `Started` and `Completed` (priority=1,
  surfaced with `-o wide`) alongside the existing Agent / Phase / Age.
- **Structured Info log** emitted on every Task phase transition,
  carrying the before/after phase and the condition reason.

### Changed

- Trigger adapter log messages normalised to the Kubernetes logging
  style (capital first letter, no trailing period, past tense) and
  now emit an Info log on each successful Task creation.

### Known limitations

- No `ValidatingAdmissionWebhook` on the Task CRD yet — validation
  happens post-admission in the reconciler.
- No Kubernetes `Event` emission on Task phase transitions or
  policy-generation failures.
- `ClusterAgentClass.spec.image` accepts tags; digest references
  are not enforced.
- `spec.resources.cpu` and `spec.resources.memory` are optional.
- `AgentRef.config` and `AgentInputs.config` are declared in the CRD
  but not yet projected into the pod. Planned for post-v0.1.0
  alongside multi-agent support (Gemini, Codex, etc.).
- NetworkPolicy egress resolves DNS names to IPs at reconcile time
  (no dynamic FQDN enforcement). See ADR-0005 for the Cilium
  upgrade path.

## Links

- [Unreleased]: https://github.com/openconveyor/openconveyor/compare/v0.1.0...HEAD
- [0.1.0]: https://github.com/openconveyor/openconveyor/releases/tag/v0.1.0
