# OpenConveyor

> Run AI coding agents on your own Kubernetes cluster as one-shot,
> least-privilege Jobs. Any trigger. Any agent image.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/openconveyor/openconveyor)](https://goreportcard.com/report/github.com/openconveyor/openconveyor)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](#status-and-roadmap)
[![Website](https://img.shields.io/badge/website-openconveyor.ai-0A66C2.svg)](https://openconveyor.ai)

OpenConveyor is a Kubernetes operator. You describe one unit of agent
work in a `Task` resource. The Task names the agent image, the prompt,
and the exact Secrets, network hosts and cluster API verbs that the
agent can use. The controller runs that Task once as a hardened Job.
The controller derives the ServiceAccount, the NetworkPolicy and the
mounted Secrets from the Task and from nothing else. When you delete
the Task, Kubernetes deletes everything that the Task created.

## Why it exists

A coding agent is only useful when it holds real credentials. It needs
a model API key, a token that can push branches and open pull
requests, and sometimes a few cluster permissions. The usual places to
run one are a laptop, a CI runner or a long-lived pod. Each of them
gives the agent far more than one task needs. The agent can reach every
host on the network, read every environment variable, and run until
somebody notices. A hostile prompt or a compromised dependency can
trick the agent, and then the attacker inherits all of that.

Each existing option covers a different need. Hosted runners from a
model vendor tie you to that vendor's agent and cloud.
`kubernetes-sigs/agent-sandbox` targets long-lived interactive
workspaces with gVisor or Kata isolation. That is more machinery than
a one-shot job needs
([ADR-0002](docs/adr/0002-build-own-sandbox.md)). OpenConveyor fills
the gap between them. It runs one-shot agent Jobs on the cluster you
have, including a single-node k3s box, with any agent image and any
trigger.

The API server enforces a set of defaults on every run, and these
defaults make it safe. The [Security model](#security-model) section
lists them.

## How it works

```
 trigger                      Task CR                   TaskReconciler
 (GitHub, Linear, cron,  ──▶  (kubectl apply)      ──▶  (conveyor controller)
  slash command, script)                                        │
                                                                │ creates and owns
                                                                ▼
                                                  ┌───────────────────────────┐
                                                  │ Job running the agent     │
                                                  │  own ServiceAccount       │
                                                  │  own NetworkPolicy        │
                                                  │  declared Secrets as files│
                                                  └───────────────────────────┘
```

1. **Something creates a `Task`.** A webhook, a CronJob, a shell
   script or a slash command in your editor. Anything that can
   `kubectl apply` a Task is a trigger
   ([ADR-0003](docs/adr/0003-triggers-out-of-process.md)).
2. **The controller resolves the agent.** The Task names a
   `ClusterAgentClass`, which supplies the container image and the
   baseline Secrets and egress every run of that agent needs. The
   controller merges the Task's own permissions with that baseline.
3. **The controller creates the policy objects first.** It creates a
   dedicated ServiceAccount with token automount disabled and a
   NetworkPolicy. It creates a Role and a RoleBinding only when the
   Task declares `permissions.rbac`. The NetworkPolicy always lands
   before the Job, because some CNIs leave a short default-allow window
   for pods that start before their policy exists.
4. **The Job runs once.** The controller mounts the prompt at
   `/task/prompt` and each declared Secret at
   `/run/secrets/<name>/<key>`. The pod runs under the Pod Security
   Standards "restricted" profile. `timeout` becomes
   `activeDeadlineSeconds` and `backoffLimit` is 0.
5. **Status follows the Job.** `status.phase` moves from Pending to
   Running and ends in Completed, Failed or TimedOut. Conditions and
   Kubernetes Events carry the detail, so `kubectl describe task`
   tells the whole story.
   [`docs/status-reference.md`](docs/status-reference.md) lists every
   phase, reason and Event.
6. **The Task goes away when you say so.** Set
   `spec.ttlSecondsAfterFinished` on the Task, or `--task-ttl-seconds`
   on the controller, and the controller deletes a finished Task after
   that delay. Unset at both levels keeps the Task forever.

Every object the controller creates has an owner reference to the
Task and the label `openconveyor.ai/task=<name>`. One `kubectl get -l`
shows the whole footprint of a run, and one `kubectl delete task`
removes it.

### The three resources

**`Task`** is what most users touch. It is in a namespace and
describes one run:

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: Task
metadata:
  name: fix-nil-pointer
spec:
  agent:
    ref: claude-code-implementer
  prompt:
    inline: |
      Fix the nil-pointer dereference on line 42 of foo.go and open a PR
      against the `main` branch.
  permissions:
    # api.anthropic.com and anthropic-api-key come from the AgentClass.
    secrets: [github-token]
    egress:  [api.github.com, github.com]
  resources:
    cpu:     "1"
    memory:  1Gi
    timeout: 30m
  ttlSecondsAfterFinished: 86400   # optional: delete the Task a day after it finishes
```

**`ClusterAgentClass`** describes an agent: an image, where it expects
the prompt, and what every run of it requires. Any container that
reads `/task/prompt` and exits is a valid agent. The repo ships
reference classes for an implementer, a reviewer and a reporter built
on Claude Code ([`config/samples/`](config/samples/)).

**`ClusterTriggerClass`** configures the optional built-in webhook
adapter. It sets an HTTP path, an HMAC signature to verify, payload
filters, and a Task template to fill from the payload. It is the only
trigger that is in the OpenConveyor binary.

## Quick start

You need `kubectl` and a Kubernetes 1.29+ cluster. The CEL validation
rules on the CRDs require version 1.29. You also need an Anthropic API
key. The sample Task also declares a `github-token` Secret, because the
implementer agent mounts it for the repository flows in
[`examples/`](examples/). Any non-empty value works for the smoke test
below.

```sh
# 1. Install CRDs and the controller into conveyor-system.
kubectl apply -f https://github.com/openconveyor/openconveyor/releases/download/v0.2.1/install.yaml

# 2. Create the Task namespace and the Secrets the sample Task mounts.
#    Secrets are projected as files at /run/secrets/<name>/<key>; the
#    reference agents export them as ANTHROPIC_API_KEY / GITHUB_TOKEN.
kubectl create namespace conveyor-tasks
kubectl -n conveyor-tasks create secret generic anthropic-api-key \
  --from-literal=key="$ANTHROPIC_API_KEY"
kubectl -n conveyor-tasks create secret generic github-token \
  --from-literal=token="${GITHUB_TOKEN:-unused}"

# 3. Apply the sample AgentClasses and a Task, then watch it run.
kubectl apply -k https://github.com/openconveyor/openconveyor//config/samples?ref=v0.2.1
kubectl -n conveyor-tasks get tasks -w
```

The Task goes from `Pending` to `Running` to `Completed` in about a
minute. The first run pulls the agent image, so it takes longer. Read
the agent output with `kubectl -n conveyor-tasks logs job/task-sample`.
Run `kubectl -n conveyor-tasks describe task task-sample` to see the
Events. Every phase and reason is in
[`docs/status-reference.md`](docs/status-reference.md). The
[shared prerequisites](examples/README.md#shared-prerequisites) in
`examples/` describe the same Secrets for the full examples.

**Helm.** The release workflow publishes the chart as an OCI artifact
and attaches it to each release as a `.tgz`. The chart installs the
CRDs and keeps them on uninstall unless you set `crd.keep=false`.

```sh
helm install conveyor oci://ghcr.io/openconveyor/charts/conveyor \
  --version 0.2.1 \
  --namespace conveyor-system --create-namespace
```

**From source.** `make install` applies the CRDs and `make run` runs
the controller against your current kubeconfig. To deploy your own
build, set `IMG` and run `make docker-build docker-push deploy`.

**Webhook triggers.** Start the manager with
`--trigger-bind-address=:9090` to enable the adapter. It reads
`ClusterTriggerClass` objects and their signing Secrets from the
namespace given by `--trigger-namespace` (default `conveyor-system`).

**Controller flags.**

| Flag | Default | Helm value | What it does |
|---|---|---|---|
| `--trigger-bind-address` | blank (disabled) | none | Starts the webhook trigger adapter on this address. Use it with `--trigger-namespace`, the namespace that holds the HMAC Secrets. |
| `--egress-refresh-interval` | `5m` | none | Re-resolves the egress hostnames while a Task runs, so the NetworkPolicy follows DNS changes. `0` disables the refresh. |
| `--job-ttl-seconds` | `3600` | `manager.jobTTLSeconds` | Kubernetes keeps a finished Job and its logs for this many seconds. |
| `--task-ttl-seconds` | unset | `manager.taskTTLSeconds` | The controller deletes a terminal Task after this many seconds. The Task's own `spec.ttlSecondsAfterFinished` wins. Unset at both levels keeps the Task. |

## Examples

The reconciler, the AgentClasses and the Task shape are identical in
every example. Only the trigger changes.

| Example | Trigger | What it shows |
|---|---|---|
| [`github-issues-claude-code/`](examples/github-issues-claude-code/) | GitHub Issues webhook | Label an issue, get a PR |
| [`github-pr-claude-code/`](examples/github-pr-claude-code/) | GitHub Pull Requests webhook | Label a PR, get a review comment |
| [`linear-claude-code/`](examples/linear-claude-code/) | Linear workflow-state webhook | SaaS tracker as the trigger |
| [`cronjob-reporter/`](examples/cronjob-reporter/) | Kubernetes CronJob | Scheduled report posted as an issue comment |
| [`claude-code-slash-dispatch/`](examples/claude-code-slash-dispatch/) | Claude Code `/conveyor` command | Your laptop is the trigger, no server needed |

## Security model

Four guarantees, each enforced by a Kubernetes primitive rather than
by documentation or by the agent image:

1. **Default-deny egress.** No declared egress means no network at
   all, including DNS, so a prompt-injected agent cannot exfiltrate to
   a host nobody approved. The controller resolves the declared hosts
   at reconcile time and opens them on port 443 only, unless
   `egressPorts` says otherwise.
2. **Default-zero RBAC.** The controller does not mount the
   ServiceAccount token and gives the ServiceAccount no bindings unless
   the Task declares `permissions.rbac`. A compromised agent has no
   ambient path to the cluster API.
3. **Declared Secrets only.** Mounted as read-only files, never as
   environment variables, so a child process that dumps its
   environment leaks nothing.
4. **Hard kill.** `timeout` is mandatory and becomes
   `activeDeadlineSeconds`, and there are no retries, so a runaway
   agent cannot burn tokens indefinitely.

The pod itself runs as non-root with a read-only root filesystem, all
capabilities dropped and the default seccomp profile. The threat
model, what is deliberately out of scope, and commands to verify each
guarantee from inside a pod are in
[`docs/security-model.md`](docs/security-model.md).
[`docs/adr/`](docs/adr/) records the reasoning behind each decision.

## Status and roadmap

OpenConveyor is alpha. The current release is v0.2.1. Unit and envtest
integration tests cover the security baseline and the trigger adapter.
The controller exports Prometheus metrics for phase transitions,
reconcile errors, run duration and webhook outcomes. The APIs can
change before 1.0.

Next up:

- A `conveyor-git` helper that gives reference agents one interface
  over the GitHub, GitLab and Forgejo CLIs.
- GitLab and Forgejo trigger examples.
- Reference agent images for other models, built on the
  `AgentRef.config` projection that shipped in v0.2.0.

## Community

- **Website:** [openconveyor.ai](https://openconveyor.ai)
- **Issues and discussions:** GitHub Issues on this repository
- **Security:** please report vulnerabilities through a
  [GitHub security advisory][sec] instead of a public issue

[sec]: https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability

## Contributing

Issues, design critique and PRs are welcome. Useful reading before
you start:

- [`docs/architecture.md`](docs/architecture.md) explains the
  reconciliation flow and which package owns what.
- [`docs/status-reference.md`](docs/status-reference.md) lists every
  phase, `Ready` reason and Event that a Task can report, for triggers
  and dashboards.
- [`docs/plugin-authoring.md`](docs/plugin-authoring.md) describes the
  agent contract, the `ClusterAgentClass` and `ClusterTriggerClass`
  fields, and the environment variables of the reference agents.
- [`docs/enterprise-auth.md`](docs/enterprise-auth.md) shows how to
  give agents short-lived credentials with External Secrets.
- [`docs/adr/`](docs/adr/) holds the required decisions. A PR that
  argues against one must propose a superseding ADR.
- [`AGENTS.md`](AGENTS.md) lists the kubebuilder scaffolding
  conventions the repo follows.

## License

OpenConveyor uses the [Apache License, Version 2.0](LICENSE).
