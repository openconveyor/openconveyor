# Architecture Decision Records

This directory records the design choices behind OpenConveyor that are
not obvious. Without a record, a new contributor would have to
reverse-engineer them from the code or the PRs.

## What an ADR is for here

Write an ADR when:

- You selected one approach and rejected a reasonable alternative,
  and a reader of the code would not know why.
- You adopt a convention that reviewers cite in code review
  ("we do not do X here, see ADR-000N").
- Multiple phases of the roadmap need the decision, and future work
  depends on it.

Do **not** write an ADR for:

- Trivial style choices. The linter handles those.
- Decisions that follow directly from the framework (for example "use
  controller-runtime"; kubebuilder selected that for us).
- Implementation details that one file shows.

## Lifecycle

- **Proposed** — the discussion is in progress. The decision can change.
- **Accepted** — we follow it. Code and docs reference it.
- **Superseded by ADR-N** — a later ADR overrules it. It stays in place
  so that future readers can see the trail.
- **Deprecated** — the thing it governs no longer exists.

Never delete an accepted ADR. If we change our minds, add a new ADR
that supersedes the old one. Then update the status line of the old
one to point at the new one. A prose edit that keeps the decision does
not change the Status or Date lines. The Simplified Technical English
pass of 2026-10-26 is one example.

## Numbering

Numbers are four digits, zero-padded, and assigned in order: `0001-`,
`0002-`, and so on. The number is permanent. You can edit the title in
the filename when you rename an ADR, but the number stays.

## Writing one

Copy [`template.md`](template.md) to `NNNN-short-kebab-title.md`,
fill it in, and add an entry to the index below. Keep it short. The
point of an ADR is that a reviewer can grasp the decision in two
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
| [0007](0007-simplified-technical-english.md) | Write all prose in Simplified Technical English | Accepted |
