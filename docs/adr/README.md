# Architecture Decision Records

This directory captures the non-obvious design choices behind
OpenConveyor — the ones a new contributor would otherwise have to
reverse-engineer from code or PRs.

## What an ADR is for here

Write an ADR when:

- You picked one approach and rejected a reasonable alternative,
  and the reader of the code wouldn't know why.
- You're adopting a convention that will be cited in code review
  ("we don't do X here — see ADR-000N").
- A decision is load-bearing across multiple phases of the roadmap
  and future work will depend on it.

Do **not** write an ADR for:

- Trivial style choices (linter handles those).
- Decisions that flow obviously from the framework (e.g. "use
  controller-runtime" — kubebuilder picked that for us).
- Implementation details visible in one file.

## Lifecycle

- **Proposed** — discussion in flight. May change.
- **Accepted** — we are living by it. Referenced by code/docs.
- **Superseded by ADR-N** — overruled by a later ADR; left in place
  so future readers can see the trail.
- **Deprecated** — the thing it governs no longer exists.

Never delete an accepted ADR. If we change our minds, add a new
ADR that supersedes the old one, and update the old one's status
line to point at it.

## Numbering

Four-digit, zero-padded, monotonically assigned: `0001-`, `0002-`,
… The number is forever; the title in the filename can be edited
if we rename, but the number stays.

## Writing one

Copy [`template.md`](template.md) to `NNNN-short-kebab-title.md`,
fill it in, and add an entry to the index below. Keep it short —
the point of an ADR is so a reviewer can grasp the decision in two
minutes.

## Index

| # | Title | Status |
|---|---|---|
| [0001](0001-one-crd-task.md) | One CRD (`Task`), not a `Task`/`TaskRun` split | Accepted |
| [0002](0002-build-own-sandbox.md) | Build our own sandbox primitive; do not adopt `kubernetes-sigs/agent-sandbox` | Accepted |
| [0003](0003-triggers-out-of-process.md) | Triggers are out-of-process; anything that creates a Task CR is a trigger | Accepted |
| [0004](0004-secrets-as-files.md) | Project secrets as files, never as env vars | Accepted |
| [0005](0005-default-deny-with-empty-egress.md) | Empty `permissions.egress` means deny-all — no implicit DNS | Accepted |
| [0006](0006-webhook-adapter-trust-boundary.md) | Webhook adapter trust boundary — HMAC, capped body, namespace-local secrets | Accepted |
