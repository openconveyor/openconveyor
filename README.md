# OpenConveyor

> Run AI coding agents on your own Kubernetes cluster as one-shot,
> least-privilege Jobs. Any trigger. Any agent image.

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)
[![Go Report](https://img.shields.io/badge/go%20report-pending-lightgrey.svg)](#)
[![Status](https://img.shields.io/badge/status-alpha-orange.svg)](#status-and-roadmap)
[![Website](https://img.shields.io/badge/website-openconveyor.ai-0A66C2.svg)](https://openconveyor.ai)

OpenConveyor is a Kubernetes operator. You describe a unit of agent
work in a `Task` resource: which agent image to run, what prompt to
give it, and exactly which Secrets, network hosts and cluster API
verbs it may use. The controller runs that Task once as a hardened
Job whose ServiceAccount, NetworkPolicy and mounted Secrets are
derived from the Task and nothing else. Delete the Task and
everything it created is garbage-collected with it.

## Why it exists

A coding agent is only useful when it holds real credentials: a model
API key, a token that can push branches and open pull requests, maybe
a few cluster permissions. The usual places to run one, a laptop, a CI
runner or a long-lived pod, hand it far more than any single task
needs. It can reach every host on the network, read every environment
variable, and keep running until somebody notices. An agent that is
tricked by a hostile prompt or a compromised dependency inherits all
of that.

Existing options each cover a different slice. Hosted runners from a
model vendor tie you to that vendor's agent and cloud.
`kubernetes-sigs/agent-sandbox` targets long-lived interactive
workspaces with gVisor or Kata isolation, which is more machinery than
a fire-and-forget job needs
([ADR-0002](docs/adr/0002-build-own-sandbox.md)). OpenConveyor fills
the gap in between: one-shot agent runs on whatever cluster you
already have, including a single-node k3s box, with any agent image
and any trigger. What makes that safe is a set of defaults the API
server enforces on every run, listed under
[Security model](#security-model).

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
   Task's own permissions are unioned with that baseline.
3. **Policy objects are created first.** A dedicated ServiceAccount
   with token automount disabled, a NetworkPolicy, and a Role plus
   RoleBinding only if `permissions.rbac` is set. The NetworkPolicy
   always lands before the Job, because some CNIs leave a brief
   default-allow window for pods that start before their policy exists.
4. **The Job runs once.** The prompt is mounted at `/task/prompt`,
   each declared Secret at `/run/secrets/<name>/<key>`, and the pod
   runs under the Pod Security Standards "restricted" profile.
   `timeout` becomes `activeDeadlineSeconds` and `backoffLimit` is 0.
5. **Status follows the Job.** `status.phase` moves from Pending to
   Running and ends in Completed, Failed or TimedOut. Conditions and
   Kubernetes Events carry the detail, so `kubectl describe task`
   tells the whole story.

Every object the controller creates has an owner reference to the
Task and the label `openconveyor.ai/task=<name>`, so one `kubectl
get -l` shows a run's whole footprint and one `kubectl delete task`
removes it.

### The three resources

**`Task`** is what most users touch. It is namespaced and describes
one run:

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
```

**`ClusterAgentClass`** describes an agent: an image, where it expects
the prompt, and what every run of it requires. Any container that
reads `/task/prompt` and exits is a valid agent. The repo ships
reference classes for an implementer, a reviewer and a reporter built
on Claude Code ([`config/samples/`](config/samples/)).

**`ClusterTriggerClass`** configures the optional built-in webhook
adapter: an HTTP path, an HMAC signature to verify, payload filters,
and a Task template to fill from the payload. It is the only trigger
that lives inside the OpenConveyor binary.

## Quick start

You need `kubectl` and a Kubernetes 1.29+ cluster. Version 1.29 is
required for the CEL validation rules on the CRDs.

```sh
# Install CRDs and the controller into conveyor-system.
kubectl apply -f https://github.com/openconveyor/openconveyor/releases/download/v0.2.1/install.yaml

# Apply the sample AgentClasses and a Task, then watch it run.
kubectl apply -k https://github.com/openconveyor/openconveyor//config/samples?ref=v0.2.1
kubectl get tasks -w
```

The sample Task expects `anthropic-api-key` and `github-token`
Secrets in the `conveyor-tasks` namespace; see the
[shared prerequisites](examples/README.md#shared-prerequisites).

**Helm.** The chart is published as an OCI artifact and attached to
each release as a `.tgz`. CRDs install with the chart and are kept on
uninstall unless you set `crd.keep=false`.

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
   a host nobody approved. Declared hosts are resolved at reconcile
   time and allowed on port 443 only, unless `egressPorts` says
   otherwise.
2. **Default-zero RBAC.** The ServiceAccount token is not mounted and
   the ServiceAccount has no bindings unless `permissions.rbac` is set,
   so a compromised agent has no ambient path to the cluster API.
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
[`docs/security-model.md`](docs/security-model.md). The reasoning
behind each decision is recorded in [`docs/adr/`](docs/adr/).

## Status and roadmap

OpenConveyor is alpha; the current release is v0.2.1. The security
baseline and the trigger adapter are covered by unit and envtest
integration tests, and the controller exports Prometheus metrics for
phase transitions, reconcile errors, run duration and webhook
outcomes. APIs may still change before 1.0.

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
- [`docs/plugin-authoring.md`](docs/plugin-authoring.md) covers adding
  a new AgentClass or ClusterTriggerClass.
- [`docs/enterprise-auth.md`](docs/enterprise-auth.md) shows how to
  give agents short-lived credentials via External Secrets.
- [`docs/adr/`](docs/adr/) holds the load-bearing decisions. A PR that
  argues against one should propose a superseding ADR.
- [`AGENTS.md`](AGENTS.md) lists the kubebuilder scaffolding
  conventions the repo follows.

## License

OpenConveyor is licensed under the [Apache License, Version 2.0](LICENSE).
