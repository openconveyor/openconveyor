# OpenConveyor

> Kubernetes-native orchestrator for AI agents.
> Least-privilege by default. Trigger-agnostic. Agent-agnostic.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Report](https://img.shields.io/badge/go%20report-pending-lightgrey.svg)](#)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](#status)
[![Website](https://img.shields.io/badge/website-openconveyor.ai-0A66C2.svg)](https://openconveyor.ai)

**OpenConveyor** runs AI coding agents as one-shot Kubernetes Jobs,
locked down to the exact secrets, egress targets, and cluster
permissions they need — nothing more. A Linear ticket, a GitHub issue,
a cron schedule, a slash command in your editor — anything that can
create a `Task` CR becomes a trigger. Any container that reads a
prompt and exits is a valid agent.

---

## Table of contents

- [Why OpenConveyor](#why-openconveyor)
- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Examples](#examples)
- [Architecture](#architecture)
- [Security model](#security-model)
- [Roadmap](#roadmap)
- [Status](#status)
- [Community](#community)
- [Contributing](#contributing)
- [License](#license)

---

## Why OpenConveyor

The Kubernetes-plus-AI-agent landscape has platforms (OpenHands),
lower-level sandbox primitives (`kubernetes-sigs/agent-sandbox`), and
vendor-coupled products (hosted Claude runners, GitHub Actions). None
of them target the specific slice this project cares about:

- **Run on the cluster you already have.** k3s on a homelab box.
  No cloud account, no SaaS dependency.
- **The agent you prefer.** Claude Code, a fine-tuned local model,
  or something the community ships tomorrow. The orchestrator
  doesn't care.
- **The trigger you already use.** Linear, GitHub, GitLab, Forgejo,
  a cron, a Telegram bot, a slash command in Claude Code.
- **With the permissions you explicitly declared.** Default egress is
  deny-all. Default RBAC is zero. Default secrets list is empty. The
  Task spec is the single source of truth for what an agent can reach.

If any of those four points matches how you want to run agents,
OpenConveyor was built for you.

## How it works

One CRD, `Task`, describes what you want to run:

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: Task
metadata:
  name: fix-null-check
spec:
  agent:
    ref: claude-code-implementer
  prompt:
    inline: "Fix the null-pointer on line 42 of pkg/foo/bar.go and open a PR."
  permissions:
    secrets: [anthropic-api-key, github-token]
    egress:  [api.anthropic.com, api.github.com, github.com]
  resources:
    cpu:     "1"
    memory:  1Gi
    timeout: 30m
```

A controller reconciles the Task into a hardened set of owned
resources: a dedicated `ServiceAccount`, a default-deny
`NetworkPolicy` with the declared egress allowlist, optional `Role`
+ `RoleBinding` only when RBAC is requested, projected `Secret`
volumes, and a one-shot `Job` running under Pod Security Standards
"restricted" defaults. Delete the Task and everything it spawned
gets garbage-collected with it.

## Quick start

Requirements: `kubectl`, a Kubernetes cluster (k3s / kind / EKS / anything).

```sh
# 1. Install CRDs + controller. Pulls the v0.1.0 multi-arch image
#    (linux/amd64 + linux/arm64) from ghcr.io/openconveyor/conveyor.
kubectl apply -f https://github.com/openconveyor/openconveyor/releases/download/v0.1.0/install.yaml

# 2. Apply the sample AgentClass + a Task.
kubectl apply -k https://github.com/openconveyor/openconveyor//config/samples?ref=v0.1.0
kubectl get tasks -w
```

The controller lands in the `conveyor-system` namespace. Sample Tasks
land in `conveyor-tasks`. See [`docs/security-model.md`](docs/security-model.md)
for the four guarantees the controller enforces on every Task it spawns.

### Building from source

For contributors and forks:

```sh
git clone https://github.com/openconveyor/openconveyor.git
cd openconveyor

# Run the controller from your laptop against the current kubeconfig:
make install   # apply CRDs
make run       # run the controller in the foreground

# Or build + push your own image and deploy it inside the cluster:
export IMG=<registry>/conveyor:<tag>
make docker-build docker-push IMG=$IMG
make deploy                   IMG=$IMG
```

Webhook trigger adapter (optional, enables external triggers):

```sh
# Any replica accepts webhooks; the controller and adapter share a manager.
conveyor --trigger-bind-address=:9090 --trigger-namespace=conveyor-system
```

## Examples

| Example | Trigger | What it proves |
|---|---|---|
| [`examples/github-issues-claude-code/`](examples/github-issues-claude-code/) | GitHub Issues webhook | Label → Task → PR |
| [`examples/linear-claude-code/`](examples/linear-claude-code/) | Linear workflow-state webhook | SaaS tracker → Task → PR |
| [`examples/github-pr-claude-code/`](examples/github-pr-claude-code/) | GitHub Pull Requests webhook | Label → Task → PR review comment (reviewer archetype) |
| [`examples/claude-code-slash-dispatch/`](examples/claude-code-slash-dispatch/) | Claude Code `/conveyor` slash command | Laptop is a trigger — no webhook, no server |

The first three examples drive the `claude-code-implementer` agent;
the PR example drives `claude-code-reviewer`. The trigger is the only
moving piece — the Task shape and the reconciler are identical.

## Architecture

```
            ┌──────────────────────┐
 trigger ──▶│  kubectl apply Task  │
            └──────────┬───────────┘
                       ▼
              ┌────────────────┐
              │ TaskReconciler │    Creates + owns:
              └────────┬───────┘      · ServiceAccount
                       │              · Role + RoleBinding   (if rbac declared)
                       ▼              · NetworkPolicy        (default-deny egress)
              ┌────────────────┐      · Secret volumes       (declared list only)
              │ hardened Job   │      · ConfigMap            (inline prompt)
              │  (agent image) │      · Job                  (runAsNonRoot, no caps)
              └────────────────┘
```

The trigger webhook adapter runs as a `manager.Runnable` inside the
same binary when enabled, so a single deployment hosts both the
reconciler and the HTTP endpoint.

See [`docs/architecture.md`](docs/architecture.md) for the full
reconciliation flow and code map.

## Security model

Four guarantees, each enforced by a Kubernetes primitive rather than
documentation or convention:

1. **Default-deny egress.** Zero egress declared → zero network
   reachability, including DNS. When egress is declared, DNS to
   `kube-dns` is added so hostname entries resolve.
2. **Default-zero RBAC.** `automountServiceAccountToken=false`
   unless the Task explicitly declares `permissions.rbac`.
3. **Declared secrets only.** Secrets are projected as read-only
   files at `/run/secrets/<name>`. Never as env vars (env leaks via
   `/proc` and child processes).
4. **Hard kill.** `spec.resources.timeout` is mandatory; it maps to
   `Job.spec.activeDeadlineSeconds`. `backoffLimit: 0` — no blind
   retries on expensive agent runs.

Full threat model, verification commands, and scope boundaries live
in [`docs/security-model.md`](docs/security-model.md). The rationale
behind each decision is in [`docs/adr/`](docs/adr/).

## Roadmap

OpenConveyor is in alpha and moving fast. The next milestones:

- ✅ **Phase 0–5** — Security baseline, AgentClass resolution with
  prompt projection, reference implementer agent image, HMAC
  webhook adapter, three trigger examples.
- ✅ **Phase 6** — Reviewer agent (PR labeled → review comments),
  with a GitHub Pull Requests example trigger.
- ✅ **Phase 7** — Scheduled tasks via stock `CronJob`, reporter
  agent archetype, phone workflow documentation.
- ✅ **Phase 8** — Prometheus metrics (task phase transitions,
  reconcile errors, task duration, webhook request outcomes),
  structured K8s-style logs, Task `status.observedGeneration` and
  start/complete print columns.
- ⏳ **Phase 9** — v0.1.0 release (GitHub-only scope).

**Post-v0.1.0:**

- `conveyor-git` helper unifying GitHub / GitLab / Forgejo CLIs
  inside reference agents.
- GitLab / Forgejo examples proving git-host pluggability.
- `AgentRef.config` projection into the pod (multi-agent support
  for Gemini, Codex, etc.).

## Status

**Alpha.** Phases 0–8 of the roadmap are live; the security baseline
and trigger adapter are covered by unit and `envtest` integration
tests, and the controller exposes custom Prometheus metrics on the
manager's `/metrics` endpoint. APIs may still change before v0.1.0.
No production users yet — this is pre-1.0 by design.

## Community

- **Website:** [openconveyor.ai](https://openconveyor.ai)
- **Issues & discussions:** GitHub Issues on this repository
- **Security:** Please open a [GitHub security advisory][sec]
  rather than filing a public issue for vulnerabilities

[sec]: https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability

## Contributing

We welcome issues, design critique, and PRs. Start by reading:

- [`docs/architecture.md`](docs/architecture.md) — how the operator
  is wired.
- [`docs/plugin-authoring.md`](docs/plugin-authoring.md) — how to add
  a new AgentClass or ClusterTriggerClass config.
- [`docs/adr/`](docs/adr/) — load-bearing design decisions. If your
  PR argues against one, please propose a superseding ADR.
- [`AGENTS.md`](AGENTS.md) — kubebuilder scaffolding conventions the
  repo follows.

## License

OpenConveyor is licensed under the [Apache License, Version 2.0](LICENSE).
