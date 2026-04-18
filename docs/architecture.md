# Architecture

OpenConveyor is a single controller-runtime manager reconciling one CRD
(`Task`) into five owned resources. Everything else in the repo is
either (a) a pure function that generates a piece of a Kubernetes
object, or (b) a reference trigger/agent that lives alongside the
core but shares no runtime with it.

## Reconciliation flow

```
┌────────┐   fetch   ┌──────────────────┐   validate  ┌───────────────┐
│  Task  ├──────────▶│  TaskReconciler  ├────────────▶│ markInvalid   │ → Failed
└────────┘           └──────────────────┘             └───────────────┘
                              │
                              │ resolve ClusterAgentClass
                              ▼
                     ┌──────────────────┐
                     │ ensureSA         │ → always, automount=false on SA
                     │ ensureRole       │ → only if permissions.rbac non-empty
                     │ ensureRoleBinding│ → same
                     │ ensureNP         │ → always; deny-all when egress empty
                     │ ensureJob        │ → hardened PodSpec; immutable after create
                     └──────────────────┘
                              │
                              ▼
                     ┌──────────────────┐
                     │ syncStatus       │ → derivePhase from Job conditions
                     └──────────────────┘
```

Ordering matters: the NetworkPolicy is created before the Job, so
the CNI has the policy in place before the first pod is scheduled.
Without that ordering, pods get a short default-allow window on some
CNIs before the policy is picked up.

## Code map

| Concern | Path |
|---|---|
| CRD types | `api/v1alpha1/` |
| Reconciler | `internal/controller/task_controller.go` |
| Resource builders (Job, SA) | `internal/controller/build.go` |
| Policy generators (pure) | `internal/policy/` |
| NetworkPolicy | `internal/policy/networkpolicy.go` |
| Role + RoleBinding | `internal/policy/rbac.go` |
| Secret volume projection | `internal/policy/secrets.go` |
| Shared labels | `internal/policy/labels.go` |
| Manager entrypoint | `cmd/main.go` |

The policy package is deliberately pure — no cluster I/O apart from
DNS resolution via an injectable `Resolver` interface. That is what
makes it table-testable against fixed inputs.

## Resource ownership

Every materialized object has an `ownerReference` back to the
`Task`, so deleting the Task cascades to its SA / Role / RoleBinding
/ NetworkPolicy / Job via the stock Kubernetes garbage collector.
The reconciler never writes out-of-band; it always goes through
`controllerutil.SetControllerReference` before create.

The shared label schema (`internal/policy/labels.go`) lets operators
`kubectl get all,networkpolicies,roles,rolebindings -l
openconveyor.ai/task=<name>` to see everything for a Task at once.

## Status model

`Task.status.phase` is a coarse lifecycle marker:

- **Pending** — Task accepted, Job not yet started.
- **Running** — Job has active pods or a start time.
- **Completed** — Job reported `JobComplete`.
- **Failed** — Job reported `JobFailed` with non-deadline reason, or
  the spec failed validation.
- **TimedOut** — Job reported `JobFailed` with `DeadlineExceeded`.

The richer signal is `status.conditions[type=Ready]` with a reason
from a small closed enum (`AgentClassMissing`, `InvalidSpec`,
`EgressResolveFailed`, …). Triggers should watch conditions, not
scrape phase strings.

## What lives outside the controller

Triggers and agents are out of process. They touch only the
Kubernetes API, never the controller directly:

- **Triggers** create Task CRs. The webhook adapter (Phase 4) ships
  as a separate container in the same manager binary; other triggers
  (cron, slash-command, custom scripts) have no OpenConveyor-side code
  at all.
- **Agents** are plain container images referenced by a
  `ClusterAgentClass`. The contract is "read the prompt at
  `/task/prompt`, exit with the right code." Anything more is
  agent-specific.

The intent is that adding a new trigger or a new agent never requires
editing anything in `internal/`.
