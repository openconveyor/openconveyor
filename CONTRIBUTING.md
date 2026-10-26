# Contributing to OpenConveyor

Thank you for your interest in OpenConveyor. The project is in alpha. We
welcome issues, design critique, and PRs.

## Before you start

- Read [`docs/architecture.md`](docs/architecture.md) to learn how the
  parts of the operator connect, and [`docs/security-model.md`](docs/security-model.md)
  for the guarantees that the project must preserve.
- Check the [`docs/adr/`](docs/adr/) directory. If your change
  argues against an accepted ADR, open a PR that supersedes the ADR.
  Do not diverge from it in silence.
- [`AGENTS.md`](AGENTS.md) documents the Kubebuilder conventions that this
  repository follows. Do not hand-edit generated files.

## Local development

```sh
make test          # unit + envtest integration tests
make lint          # golangci-lint with the repo's custom linter plugin
make lint-docs     # Vale: Simplified Technical English rules, warnings only
make manifests     # regenerate CRDs and RBAC after editing *_types.go
make generate      # regenerate DeepCopy methods
make run           # run the controller against your current kubeconfig
```

The `.devcontainer/` directory contains a dev container for VS Code users.

## Pull requests

- Target `main`.
- Keep changes focused. One PR, one reason.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`.
- Include tests. The envtest suite under
  `internal/controller/*_test.go` and the unit suites under
  `internal/policy/` and `internal/trigger/` are the models.
- Update the docs when the behavior changes. If you change a
  security-relevant default, update `docs/security-model.md` in the same PR.
- Do not write generated code by hand. Run `make manifests generate`
  and commit the diff.
- Write prose in Simplified Technical English. See
  [`docs/writing-style.md`](docs/writing-style.md) and run
  `make lint-docs`.

## Reporting bugs

Open a GitHub issue with the bug-report template. For a vulnerability,
do not open a public issue. Follow [`SECURITY.md`](SECURITY.md).

## Code of conduct

The [Contributor Covenant](CODE_OF_CONDUCT.md) governs participation in
this project.
