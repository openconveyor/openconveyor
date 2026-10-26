# ADR-0001: One CRD (`Task`), not a `Task`/`TaskRun` split

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

Most execution-style operators split a reusable template CRD from an
execution CRD. Tekton has `Task` + `TaskRun`; Argo Workflows has
`WorkflowTemplate` + `Workflow`. The split lets a user define a
template once and start many runs from it.

The core object of OpenConveyor is "one agent run." We had to choose
between one CRD, where a `Task` _is_ a run, and two CRDs, a `Task`
template and a `TaskRun` execution.

## Decision

We ship exactly one CRD, `Task`. Each CR represents a single run.
Users define reusable templates outside the CRD surface. For example,
a trigger reads a stock `ConfigMap`, or the `spec.task` block on a
`ClusterTriggerClass` emits a fresh Task CR for each event.

## Alternatives considered

- **`Task` + `TaskRun` (Tekton-style).** Rejected. We have no reuse
  case that a trigger cannot cover when it templates a fresh Task for
  each event. A second CRD means a second reconciler, a second status
  lifecycle, and a harder mental model for a homelab operator. That
  operator mostly runs `kubectl describe task`.
- **A single CRD with an embedded template field** (`Task.spec.template`
  that another Task can reference). Rejected. This solves the same
  reuse gap in a more confusing way. A Task that points at another
  Task is a bad copy of `TaskRun`.

## Consequences

- **Simpler mental model.** "A Task is a run": `kubectl get tasks`
  shows the history of agent runs in the cluster.
- **No template/run lineage.** A user who wants to run a past Task
  again applies the trigger again, or runs `kubectl get task <name> -o yaml | edit |
  kubectl create -f -`. This is acceptable for v1. We revisit it if we
  add a "replay this run" workflow.
- **Templates are outside the CRD schema.** The
  `ClusterTriggerClass.spec.task` of the webhook adapter holds the Task
  shapes that users reuse. Template authoring is therefore
  trigger-specific. That is fine, because triggers already are.

## References

- `api/v1alpha1/task_types.go`
