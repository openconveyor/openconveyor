# ADR-0009: Dedupe webhook Tasks by label, not by name

- **Status:** Accepted
- **Date:** 2026-10-31
- **Deciders:** Martijn Scholten

## Context

GitHub delivers one webhook for each label event and redelivers on
request. Today each delivery creates a new Task, so one issue can run
two agents at the same time and open two pull requests. We want one
active Task for each issue (and for each pull request in the iterate
flow), but a finished Task must not block a deliberate re-run. Decided
in [wayfinder ticket #106](https://github.com/openconveyor/openconveyor/issues/106).

## Decision

The webhook adapter dedupes before it creates a Task, driven by the
`ClusterTriggerClass`:

- `spec.dedupe.uniqueBy` lists label keys that the mappings populate,
  for example `openconveyor.ai/issue-number` or
  `openconveyor.ai/pr-number`.
- The adapter stamps every Task it creates with
  `openconveyor.ai/trigger-class=<name>`.
- Before `Create`, the adapter lists the Tasks in the target namespace
  with the same trigger-class label and the same `uniqueBy` values,
  through a live API read, not the cache. When one of them is not
  terminal (phase not Completed, Failed or TimedOut, an empty phase
  included), the adapter answers `202 {"duplicate": true, "task":
  "<name>"}`, records a dedicated metric result, and creates nothing.
- A terminal Task never blocks. A new label event after a run starts a
  new run.
- The check is best effort. Two deliveries that arrive within one API
  round trip can both pass. The label gate stays the primary guard
  against unwanted runs.

## Alternatives considered

- **A deterministic Task name such as `gh-issue-42`.** Rejected. The
  API server then rejects every later create until the Task is deleted,
  so a finished Task blocks a re-run until its TTL. Renaming a finished
  Task to free the name means delete and recreate.
- **A validating admission webhook on Task.** Rejected. It would
  enforce the rule for every trigger, but it adds cert-manager and an
  admission webhook to the install for a rule that only webhook
  triggers need.
- **A lock or a Lease around create.** Rejected. More machinery than
  the residual race deserves at this volume.
- **Hardcoded keys for issues and pull requests.** Rejected. Every new
  event source would need code; a list of label keys needs none.

## Consequences

- One pull request per issue over its lifetime becomes possible; the
  implementer reuses an existing PR on a re-run.
- The adapter gains `tasks` `list` through a live reader. Dedupe adds
  one API call for each matched webhook.
- Triggers that share one class across several repositories can
  collide on the same issue number. One repository per class is the
  supported shape until a repository label joins `uniqueBy`.
- A `202` now has two meanings, told apart by the body and the metric.

## References

- ADR-0006 (webhook adapter trust boundary), ADR-0001 (one CRD).
- `internal/trigger/handler.go`, `internal/trigger/mapping.go`,
  `api/v1alpha1/clustertriggerclass_types.go`.
- Research: branch `research/github-loop-facts`.
