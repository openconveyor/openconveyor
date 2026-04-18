# Contributing to OpenConveyor

Thanks for your interest in OpenConveyor. The project is in alpha — we
welcome issues, design critique, and PRs.

## Before you start

- Read [`docs/architecture.md`](docs/architecture.md) for how the
  operator is wired, and [`docs/security-model.md`](docs/security-model.md)
  for the guarantees the project has to preserve.
- Check the [`docs/adr/`](docs/adr/) directory. If your change
  argues against an accepted ADR, please open a PR that supersedes
  it rather than silently diverging.
- [`AGENTS.md`](AGENTS.md) documents the Kubebuilder conventions this
  repository follows — do not hand-edit generated files.

## Local development

```sh
make test          # unit + envtest integration tests
make lint          # golangci-lint with the repo's custom linter plugin
make manifests     # regenerate CRDs and RBAC after editing *_types.go
make generate      # regenerate DeepCopy methods
make run           # run the controller against your current kubeconfig
```

A dev container is provided in `.devcontainer/` for VS Code users.

## Pull requests

- Target `main`.
- Keep changes focused. One PR, one reason.
- Use [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`.
- Include tests. The envtest suite under
  `internal/controller/*_test.go` and the unit suites under
  `internal/policy/` and `internal/trigger/` are the models.
- Update docs when behaviour changes. If you change a security-
  relevant default, update `docs/security-model.md` in the same PR.
- Do not commit generated code by hand. Run `make manifests generate`
  and commit the diff.

## Reporting bugs

Open a GitHub issue using the bug-report template. For vulnerabilities,
do not open a public issue — follow [`SECURITY.md`](SECURITY.md).

## Code of conduct

Participation in this project is governed by the
[Contributor Covenant](CODE_OF_CONDUCT.md).
