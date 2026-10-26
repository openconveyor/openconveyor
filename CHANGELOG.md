# Changelog

All notable changes to OpenConveyor are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Breaking

- **Security:** the egress allowlist now opens **port 443 only** (TCP and
  UDP) on the resolved CIDRs instead of every port. A Task whose agent
  talks to an allowlisted host on another port (SSH to `github.com`, a
  database, a non-standard HTTPS port) must list the ports in the new
  `spec.permissions.egressPorts` field, which replaces the default for all
  entries; the DNS rule keeps port 53. (#24)

### Added

- **CI/Supply chain:** release images carry an SBOM next to the existing
  provenance attestation and are signed keyless with cosign against the
  release workflow's OIDC identity; `docs/security-model.md` has the
  `cosign verify` recipe. The release is gated on the lint, unit, e2e, chart
  and agent-image workflows (reused via `workflow_call`) instead of
  `make test` alone, and every GitHub Action is pinned to a commit SHA with
  the version as a trailing comment. (#35)
- **API/Controller:** Task garbage collection (#21). `spec.ttlSecondsAfterFinished`
  on `Task` (and on the `ClusterTriggerClass` Task template) deletes a
  terminal Task, with everything it owns, that many seconds after it reaches
  Completed, Failed or TimedOut, same semantics as the Job field. The new
  `--task-ttl-seconds` flag (Helm: `manager.taskTTLSeconds`) supplies a
  cluster-wide default; a Task's own value wins. Unset at both levels keeps
  today's behavior: Tasks are never deleted automatically. The example
  trigger templates now set `86400`.
- **Controller:** the finished-Job TTL (`Job.spec.ttlSecondsAfterFinished`)
  is the new `--job-ttl-seconds` flag instead of a compile-time constant;
  default unchanged at `3600`. The Helm chart exposes it as
  `manager.jobTTLSeconds`. (#22)
- **Controller:** hostnames in a Task's egress allowlist are re-resolved
  while the Task is not terminal and the NetworkPolicy is updated when the
  resolved IPs change, so CDN/anycast rotation no longer silently breaks
  or over-permits egress mid-run. The interval is the new
  `--egress-refresh-interval` flag (default `5m`; `0` disables). Tasks
  whose allowlist has only IPs/CIDRs are not requeued. (#25)
- **API:** `spec.permissions.egressPorts` on `Task` (and on the Task
  template of `ClusterTriggerClass`): up to 16 distinct ports in 1–65535,
  applied to every egress allowlist entry; unset means `[443]`. (#24)

- **Controller:** Kubernetes Events on the Task so `kubectl describe task`
  shows what happened (#20). A `Normal` Event per phase transition
  (`Pending`, `Running`, `Completed`) and a `Warning` for `Failed`,
  `TimedOut`, `InvalidSpec`, `EgressResolveFailed` and
  `AgentClassMissing`; reason and message match the `Ready` condition.
  The controller now needs `create`/`patch` on `events.k8s.io` Events
  instead of core `events`; the Ready condition carries a fixed message
  (`Job failed`, `Job exceeded its deadline`, …) when the Job reports
  none.
- **API:** field-level validation so malformed specs are rejected at
  admission instead of reaching the Job builder (#16). `ClusterAgentClass`:
  `image` must be non-empty and `imagePullPolicy` is one of `Always`,
  `Never`, `IfNotPresent`. `Task` and `ClusterTriggerClass` templates:
  every `egress` entry must be a DNS name, an IP address or a CIDR block
  (at most 256 entries of 253 characters), the same rule applies to
  `ClusterAgentClass.spec.requires.egress`; each `rbac` rule needs at least
  one `apiGroups`, `resources` and `verbs` entry; an inline prompt is capped
  at 256 KiB (use `secretRef`/`configMapRef` for larger bodies).
- **e2e:** two real Task scenarios on kind (#39). The positive one runs a
  busybox Task to `Completed` and asserts the owned ServiceAccount,
  deny-all NetworkPolicy and hardened Job (`backoffLimit: 0`, deadline =
  timeout, no SA token, UID 65532). The negative one, promised in
  `docs/security-model.md`, runs a Task with no egress whose agent tries
  `wget https://example.com` and asserts it never reaches `Completed`.
  While writing it we found that kindnet only enforces a policy about a
  second after the container starts; the doc now records that caveat.

### Changed

- **Agents/CI:** the three reference agent images pin their base image to a
  minor tag plus digest (`node:22.23.4-bookworm-slim@sha256:…`) and the
  Claude Code CLI to an exact version through a `CLAUDE_CODE_VERSION` build
  arg (`2.1.311`), the same in all three; `gh` stays on the GitHub CLI
  stable apt channel. Dependabot's docker entry now watches `agents/*`, and
  a new `Agent Images` workflow builds the three images on every PR and runs
  `claude --version` inside each, without pushing. (#36)
- **Security:** the Task's ServiceAccount now sets
  `automountServiceAccountToken` to the same value as the Job pod (`false`
  unless `spec.permissions.rbac` is declared), and the reconciler restores
  it if changed out-of-band. Any other pod that runs as that SA gets no
  token either. No behavior change for Tasks. (#68)
- **Security:** `runAsNonRoot`, `runAsUser`/`runAsGroup` (65532) and
  `seccompProfile: RuntimeDefault` are now set on the Job's **pod**
  security context as well as on the agent container, so containers
  injected into the pod later (service-mesh sidecars) run under the same
  identity and seccomp profile, and the pod matches the Pod Security
  Standards "restricted" convention. The container block is unchanged.
  (#26)
- **e2e:** the suite no longer installs and removes cert-manager, which the
  controller does not use (about 30 s saved per run). The kind cluster is
  created from a pinned node image, `KIND_NODE_IMAGE` in the Makefile
  (default `kindest/node:v1.36.4`, bumped with the k8s deps), so local and
  CI runs test the same Kubernetes version; CI moves to kind v0.33.0.
  (#37)

## [0.2.1] — 2026-10-25

Toolchain and dependency release. No behaviour change; same CRDs,
same defaults. Rebuilt images and chart for the Go 1.25 end-of-life and
the advisories below.

### Changed

- **Toolchain:** Go 1.26 minimum, built and released with Go 1.27.3;
  controller-runtime v0.24.1 and k8s.io/* v0.36.3 (envtest 1.36).
  Builder image `golang:1.27`. Go 1.25 is end-of-life and its stdlib
  fails `govulncheck`.
- **Dependencies:** golang.org/x/net v0.61.0, google.golang.org/grpc
  v1.84.0 and go.opentelemetry.io/otel/sdk v1.45.0 for published
  advisories (GO-2026-6617, GO-2026-6612, GO-2026-6348, GO-2026-6505).
- **Tooling:** controller-tools v0.21.0, golangci-lint v2.15.0. The API
  package registers types with `runtime.NewSchemeBuilder` and no longer
  imports controller-runtime.

No behaviour change. CRDs differ only in the controller-gen annotation.

## [0.2.0] — 2026-07-27

Installable in one command:

```sh
kubectl apply -f https://github.com/openconveyor/openconveyor/releases/download/v0.2.0/install.yaml
```

Or via Helm:

```sh
helm install conveyor oci://ghcr.io/openconveyor/charts/conveyor \
  --version 0.2.0 \
  --namespace conveyor-system --create-namespace
```

Multi-arch images (`linux/amd64` + `linux/arm64`) for the controller
and the three reference agents are published to
`ghcr.io/openconveyor/{conveyor,agent-claude-implementer,agent-claude-reviewer,agent-claude-reporter}:v0.2.0`.

Requires a Kubernetes 1.29+ apiserver (CEL admission validation).

### Added

- **Helm chart** (`dist/chart/`, generated by the kubebuilder
  `helm/v2-alpha` plugin). Published on release as an OCI artifact at
  `oci://ghcr.io/openconveyor/charts/conveyor` and attached to the
  GitHub Release as a `.tgz`. Resource names match the kustomize
  install (`conveyor-` prefix with release name `conveyor`), so
  migrating an existing install is adoption, not reinstallation.
  Includes a `test-chart` CI workflow that lints and deploys the
  chart on a kind cluster.
- **`docs/enterprise-auth.md`** — short-lived agent credentials:
  GitHub App installation tokens minted by the External Secrets
  Operator `GithubAccessToken` generator, replacing static PATs.
- **Config projection** — `Task.spec.agent.config` now reaches the
  agent container: top-level scalar fields become env vars named
  after the field, and the full JSON is mounted/delivered when the
  AgentClass declares `spec.inputs.config` (`mount` and/or `env`).
  Closes the v0.1.0 known limitation. Trigger templates gained a
  `config.<KEY>` mapping target, and the GitHub-issues and
  cronjob-reporter examples now use it, so `$TARGET_REPO` /
  `$REPORT_ISSUE_URL` actually reach the reference agents.

### Changed

- **Spec validation moved to admission time.** The Task and
  ClusterTriggerClass CRDs now carry CEL `x-kubernetes-validations`
  rules: exactly one prompt source (inline / secretRef /
  configMapRef), `resources.timeout` parseable and greater than
  zero, and `resources.cpu` / `resources.memory` valid non-negative
  quantities. Invalid specs are rejected at `kubectl apply` with a
  field-level error instead of surfacing as a `Failed` Task status
  minutes later. The reconciler keeps equivalent checks as
  defense-in-depth for objects stored before this change; its prompt
  check is tightened from at-least-one to exactly-one source.
  Requires a Kubernetes 1.29+ apiserver (CEL quantity library).
- The controller no longer uses panicking `resource.MustParse` when
  building Job resource requirements; malformed quantities now
  return a permanent error that marks the Task `Failed`.
- **Production JSON logging by default.** The manager previously ran
  with zap's development encoder; pass `--zap-devel` to restore the
  human-readable output for local runs.
- The trigger adapter answers webhook requests with **500 instead of
  401** when the signature failure is the operator's misconfiguration
  (empty signing secret, unsupported algorithm). Missing or
  mismatched signatures still get 401.
- The manager's ClusterRole now includes `clustertriggerclasses`
  (get/list/watch) and `secrets` (get) — the trigger adapter needed
  both and previously hit Forbidden when enabled. The client cache is
  disabled for Secrets so no cluster-wide Secret watch is opened.

### Security

- Dependency bumps for published advisories: `google.golang.org/grpc`
  v1.82.1 (GHSA authorization bypass via `:path`, xDS RBAC/HTTP2),
  `go.opentelemetry.io/otel/sdk` v1.43.0, `github.com/google/cel-go`
  v0.29.0.

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

- [Unreleased]: https://github.com/openconveyor/openconveyor/compare/v0.2.0...HEAD
- [0.2.0]: https://github.com/openconveyor/openconveyor/releases/tag/v0.2.0
- [0.1.0]: https://github.com/openconveyor/openconveyor/releases/tag/v0.1.0
