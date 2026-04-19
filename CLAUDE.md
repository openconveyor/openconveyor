# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Read these first

- [`AGENTS.md`](AGENTS.md) — Kubebuilder scaffolding conventions, the
  list of auto-generated files that must never be hand-edited, and the
  CLI commands (`kubebuilder create api`, `kubebuilder create webhook`)
  used to add new types. **Do not duplicate or work around it.**
- [`docs/architecture.md`](docs/architecture.md) — reconciliation flow
  and the code map (which package owns what).
- [`docs/security-model.md`](docs/security-model.md) — the four
  guarantees the controller is required to preserve.
- [`docs/adr/`](docs/adr/) — load-bearing decisions (one CRD, secrets
  as files, default-deny egress, webhook trust boundary). If a change
  argues against an accepted ADR, supersede it; do not silently diverge.

## What this project is

OpenConveyor is a Kubernetes operator that runs AI coding agents as
one-shot, hardened Jobs. A single CRD (`Task`) is reconciled into a
fixed set of owned resources: ServiceAccount, NetworkPolicy, optional
Role + RoleBinding, projected Secret volumes, and a Job. Triggers
(GitHub, Linear, slash commands, …) and agent images live outside
the controller and only touch it via the Kubernetes API.

The repo is a kubebuilder v4 project, single-group layout, domain
`openconveyor.ai`, module `github.com/openconveyor/openconveyor`,
projectName `conveyor`. Three resources today: `Task` (namespaced),
`ClusterAgentClass`, `ClusterTriggerClass` (both cluster-scoped).

## Common commands

```sh
make test           # unit + envtest integration; runs manifests, generate, fmt, vet first
make test-e2e       # spins up Kind cluster `conveyor-test-e2e`, runs ./test/e2e
make lint           # custom golangci-lint (built once with the logcheck plugin from .custom-gcl.yml)
make lint-fix       # auto-fix
make run            # run controller against current kubeconfig
make install        # apply CRDs to current cluster
make deploy IMG=…   # deploy controller image
make manifests      # regenerate CRDs/RBAC after editing *_types.go or markers
make generate       # regenerate DeepCopy after editing *_types.go
make build-installer IMG=…   # writes dist/install.yaml (Kustomize bundle)
```

Run a single test package:

```sh
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use $ENVTEST_K8S_VERSION --bin-dir ./bin -p path)" \
  go test ./internal/policy/...
```

Tests use **Ginkgo + Gomega**; envtest provides a real apiserver +
etcd. Tooling (controller-gen, kustomize, setup-envtest, golangci-lint)
is downloaded into `./bin/` by the Makefile — do not install
system-wide.

## Architecture rules that aren't obvious from the file tree

- **`internal/policy/` is pure.** No cluster I/O except DNS via the
  injectable `Resolver` interface. Keep new policy generators table-
  testable; don't reach for a `client.Client` here.
- **`internal/controller/` owns I/O.** `task_controller.go` is the
  reconciler; `build.go` assembles the Job/SA. Resource builders
  always go through `controllerutil.SetControllerReference` before
  create — never write out-of-band, never skip ownership.
- **NetworkPolicy is created before the Job.** Some CNIs have a
  default-allow window before a policy is picked up; reordering
  these breaks the security model. Don't change the ensure order
  in the reconciler.
- **`automountServiceAccountToken=false`** unless `permissions.rbac`
  is non-empty. Egress empty → default-deny NetworkPolicy. Secrets
  are projected as files at `/run/secrets/<name>`, never env vars
  (ADR-0004). These defaults are guarantees, not knobs.
- **`spec.resources.timeout` is mandatory** and maps to
  `Job.spec.activeDeadlineSeconds`; `backoffLimit: 0`. No retries
  on expensive agent runs.
- **Trigger adapter is a `manager.Runnable`** in the same binary,
  enabled by `--trigger-bind-address`. The webhook trust boundary
  lives at `internal/trigger/signature.go` (HMAC) — see ADR-0006
  before touching it.
- **Shared label schema** is in `internal/policy/labels.go`. New
  owned resources should use it so `kubectl get … -l
  openconveyor.ai/task=<name>` keeps working.

## Conventions

- Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `test:`). Target `main`. One PR, one reason.
- Logging follows the K8s style guide (capital first letter, no
  trailing period, past tense, balanced key/value pairs). The
  `logcheck` linter enforces this — do not disable it.
- After editing `*_types.go` or kubebuilder markers, run
  `make manifests generate` and commit the diff. Do not hand-edit
  `zz_generated.*`, `config/crd/bases/*`, `config/rbac/role.yaml`,
  or `PROJECT`.
- If a change alters a security default, update
  `docs/security-model.md` in the same PR.
