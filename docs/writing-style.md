# Writing style: Simplified Technical English

This repository writes its prose in Simplified Technical English (STE),
the controlled language of the ASD-STE100 specification (Issue 9).
Humans and agents read the same short sentences, and Vale checks the
mechanical rules. ADR-0007 records the decision.

## Scope

The rules apply to:

- Markdown files: `README.md`, `docs/`, `examples/`, the root
  contributor files, and the GitHub templates.
- Go comments under `api/`, `cmd/`, `internal/` and `test/`, including
  the `_test.go` files.
- Comments in `agents/*/Dockerfile`, `agents/*/entrypoint.sh`, and the
  YAML under `config/samples/` and `examples/`.

The rules do not apply to:

- Log, Event and error strings. They follow the Kubernetes logging
  style guide, and `logcheck` checks them.
- `CHANGELOG.md` history, `CODE_OF_CONDUCT.md` and `LICENSE`.
- Generated files: `zz_generated.*.go`, `config/crd/bases/`,
  `config/rbac/role.yaml`, `dist/`.

## Source and copyright

ASD owns the ASD-STE100 specification and its dictionary. This guide
states the writing rules and a project word list. It does not copy the
dictionary. You can request the specification from
[asd-ste100.org](https://www.asd-ste100.org/).

## Sentence rules

1. Write one topic in each sentence.
2. Keep an instruction to 20 words or fewer. Keep a descriptive
   sentence to 25 words or fewer.
3. Keep a paragraph to one topic and six sentences or fewer. Put the
   topic sentence first.
<!-- vale STE = NO -->
4. Use the active voice and name the actor. Write "The controller
   creates the Job", not "The Job is created".
5. Use the present tense for descriptions. Use the imperative for
   instructions: "Run `make test`".
6. Use "will" only for a real future event, for example a roadmap item.
<!-- vale STE = YES -->
7. Keep the articles: "the", "a", "an". Do not remove them to save space.
8. Keep a noun cluster to three words or fewer. Write "the reason in
   the `Ready` condition", not "the Task status condition reason".
9. Use one word for one meaning. Use the terms in
   [`GLOSSARY.md`](../GLOSSARY.md) and do not use the words in its
   `_Avoid_` lists.
<!-- vale STE.Latin = NO -->
10. Do not use Latin abbreviations: `e.g.`, `i.e.`, `etc.`, `vs.`,
    `via`. Write "for example", "that is", "and more", "with".
<!-- vale STE.Latin = YES -->
<!-- vale STE.Hedging = NO -->
11. Do not use hedges or intensifiers: "simply", "just", "very",
    "obviously", "basically".
<!-- vale STE.Hedging = YES -->
12. Spell words in US English: "behavior", "authorization".

## Word list

Use the word in the right column. Vale flags the word in the left
column with the `STE.Substitution` rule.

<!-- vale STE = NO -->
| Do not use | Use |
|---|---|
| utilize | use |
| leverage | use |
| prior to | before |
| in order to | to |
| via | through, with |
| materialize | create |
| mint | create |
| spawn | create, start |
| fire (a trigger, an event) | start, send |
| hit (an endpoint) | call |
| talk to | call, connect to |
| live in, lives in | is in |
| land in, lands in | go to, is created in |
| pick up | read, apply |
| reach for | use |
| blow up | fail |
| wedge, wedged | stop, stopped |
| wide open | not restricted |
| load-bearing | required |
| knob | setting |
| bolt on | add |
| drop-in | direct |
| in-flight | in progress |
| on the fly | while it runs |
| sprinkled | present |
| w/ | with |
| gets (as "becomes") | is, becomes |
<!-- vale STE = YES -->

## Technical names and technical verbs

STE lets you use technical names and technical verbs as they are.
Write them in their usual case, and in backticks when they are code:

- Kubernetes kinds and fields: `Task`, `NetworkPolicy`,
  `spec.permissions.egress`.
- Tools, commands and flags: `kubectl`, `kind`, `controller-gen`,
  `--job-ttl-seconds`.
- File paths, image names, Go identifiers, HTTP methods and status
  codes, product names.
- Technical verbs: reconcile, requeue, apply, mount, project (a Secret),
  resolve (a DNS name), sign, verify.

## Warnings, cautions and notes

Use the GitHub alert syntax. Put one imperative sentence first, and the
consequence second.

```markdown
> [!WARNING]
> Do not set `--job-ttl-seconds=0` in production.
> Kubernetes deletes the Job and its logs as soon as the Job finishes.
```

Use `[!WARNING]` for loss of data or a security effect, `[!CAUTION]`
for an action that you cannot undo, and `[!NOTE]` for context.

## Go comments

1. Keep the godoc lead: `// Name verb ...`, `// TypeName is ...`,
   `// Package policy ...`.
2. In `api/v1alpha1/*_types.go`, start a field comment with the JSON
   field name, for example `// egress is the allowlist ...`.
   `controller-gen` copies these comments into the CRD descriptions.
3. Write the sentences after the lead in STE.
4. Use the gofmt forms for lists (`//   - item`) and for code blocks (a
   tab after `//`). Run `gofmt` after you edit a comment.
5. Keep a comment line at 120 columns or fewer.

## Text that you must not change

- `// +kubebuilder:...`, `// +optional`, `// +required`, `// +listType`
  and the other markers.
- `//go:build` and `//nolint` directives.
- The license header from `hack/boilerplate.go.txt`.
- Generated files, code, string literals, struct tags, YAML keys and
  values, and quoted command output.
- Mermaid node labels and table cells can be short noun phrases. The
  word list still applies to them.

## Diagrams

Use a fenced `mermaid` block. Do not keep an ASCII copy. GitHub renders
Mermaid; other renderers need a Mermaid plugin. Show one idea per
diagram and keep it to about 12 nodes. The sentence next to the diagram
states the rule; the diagram does not replace it.

## Check your text

Run `make lint-docs`. It runs Vale with the `STE` style on the Markdown
files and the Go comments in scope, and it reports warnings only. A
clean run shows `0 errors`. Reduce the warnings when the fix is cheap.

<!-- vale STE = NO -->
| Rule | What it flags |
|---|---|
| `STE.SentenceLength` | A sentence with more than 25 words |
| `STE.ParagraphLength` | A paragraph with more than 6 sentences |
| `STE.Passive` | A form of "to be" followed by a past participle |
| `STE.Substitution` | A word from the word list |
| `STE.Latin` | `e.g.`, `i.e.`, `etc.`, `vs.`, `via` |
| `STE.Hedging` | A hedge or an intensifier |
| `STE.FutureTense` | "will" or "won't" |
<!-- vale STE = YES -->

To silence a false positive in Markdown, wrap the lines:

```markdown
<!-- vale STE.Passive = NO -->
The pod was Evicted (the Kubernetes reason string).
<!-- vale STE.Passive = YES -->
```

<!-- vale STE = NO -->
Go comments have no inline switch. Accept the warning there. Vale also
reports the Apache license header in each Go file ("is distributed")
and the message text inside long `+kubebuilder:validation` markers.
That text is fixed; the warnings stay.
<!-- vale STE = YES -->
