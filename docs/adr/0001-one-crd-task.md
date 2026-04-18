# ADR-0001: One CRD (`Task`), not a `Task`/`TaskRun` split

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

Most execution-style operators (Tekton, Argo Workflows) split a
reusable template CRD from an execution CRD: `Task` + `TaskRun`,
`WorkflowTemplate` + `Workflow`. The split lets users define a
template once and fire many runs from it.

OpenConveyor's core object is "one agent run." We had to pick: one CRD
(a `Task` _is_ a run), or two (a `Task` template and a `TaskRun`
execution).

## Decision

We ship exactly one CRD, `Task`. Each CR represents a single run.
Reusable templates are expressed outside the CRD surface — for
example, as a stock `ConfigMap` that a trigger reads, or as the
`spec.task` block on a `ClusterTriggerClass` that emits fresh Task
CRs per event.

## Alternatives considered

- **`Task` + `TaskRun` (Tekton-style).** Rejected. We have no reuse
  case that a trigger can't already cover by templating a fresh
  Task per event. Adding a second CRD means a second reconciler,
  another status lifecycle, and a harder mental model for a homelab
  operator whose primary interaction is `kubectl describe task`.
- **A single CRD with an embedded template field** (`Task.spec.template`
  that another Task can reference). Rejected. Same reuse gap solved
  more confusingly. Once you have "a Task that points at another
  Task," you have invented `TaskRun` badly.

## Consequences

- **Simpler mental model.** "A Task is a run" — `kubectl get tasks`
  shows the history of agent runs in the cluster.
- **No template/run lineage.** Users who want to re-fire a past Task
  re-apply the trigger, or `kubectl get task <name> -o yaml | edit |
  kubectl create -f -`. Acceptable for v1; revisit if we grow a
  "replay this run" workflow.
- **Templates live outside the CRD schema.** The webhook adapter's
  `ClusterTriggerClass.spec.task` is where re-used Task shapes live.
  That means template authoring is trigger-specific; that is fine
  because triggers already are.

## References

- `api/v1alpha1/task_types.go`
