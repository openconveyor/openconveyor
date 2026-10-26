# ADR-0007: Write all prose in Simplified Technical English

- **Status:** Accepted
- **Date:** 2026-10-26
- **Deciders:** @scholtenmartijn

## Context

The prose in this repository had no shared rule. The README, the
guides, the ADRs and the Go comments mixed idioms, long sentences, the
passive voice and Latin abbreviations. Humans and coding agents write
most of this text, and both groups read it again later. A controlled
language gives them one set of rules and makes a lint check possible.

## Decision

We write all prose in Simplified Technical English (ASD-STE100,
Issue 9), as [`docs/writing-style.md`](../writing-style.md) describes
it. The decision has three parts:

1. The scope is every Markdown file, every Go comment under `api/`,
   `cmd/`, `internal/` and `test/`, and the comments in the agent
   images and the sample YAML. Log, Event and error strings keep the
   Kubernetes logging style.
2. Vale checks the mechanical rules (`make lint-docs`, the `Docs Lint`
   workflow). The check reports warnings and does not block a merge
   until the rules move to the error level in a later change.
3. A prose edit that does not change a decision does not change the
   `Status` or `Date` line of an ADR.

## Alternatives considered

- **A house style guide without a controlled language.** Rejected. A
  house guide lists preferences; it has no sentence-length rule and no
  one-word-one-meaning rule, so each writer still decides alone.
- **The Google or Microsoft developer style guide.** Rejected. Both are
  good for product documentation, but they are not controlled
  languages, and their Vale packages flag many Kubernetes identifiers.
- **No rule, review only.** Rejected. Review caught some problems and
  missed most. A rule that a tool can check scales to agents.

## Consequences

- One rewrite of about 17,000 words of prose and 800 lines of Go
  comments. After that, each change pays only for the text it touches.
- Field comments in `api/v1alpha1` become CRD descriptions, so a
  comment edit there regenerates the CRDs and the Helm chart.
- `make lint-docs` adds Vale to the local tools in `bin/`. The CI job
  annotates pull requests and does not fail them.
- ASD holds the copyright of the STE dictionary, so the guide names the
  rules and a project word list only. Writers who want the full
  dictionary request it from ASD.

## References

- [`docs/writing-style.md`](../writing-style.md)
- ASD-STE100, Issue 9: <https://www.asd-ste100.org/>
- Vale: <https://vale.sh/>
