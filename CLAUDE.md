# CLAUDE.md

This file gives guidance to Claude Code (claude.ai/code) when it works with code in this repository.

## Read these first

- [`AGENTS.md`](AGENTS.md) — the Kubebuilder scaffolding conventions, the
  list of auto-generated files that you must never hand-edit, and the
  CLI commands (`kubebuilder create api`, `kubebuilder create webhook`)
  that add new types. **Do not duplicate it or work around it.**
- [`docs/architecture.md`](docs/architecture.md) — the reconciliation flow
  and the code map (which package owns what).
- [`docs/security-model.md`](docs/security-model.md) — the four
  guarantees that the controller must preserve.
- [`docs/adr/`](docs/adr/) — the required decisions (one CRD, secrets
  as files, default-deny egress, webhook trust boundary). If a change
  argues against an accepted ADR, supersede the ADR. Do not diverge
  from it in silence.

## What this project is

OpenConveyor is a Kubernetes operator that runs AI coding agents as
one-shot, hardened Jobs. The controller reconciles a single CRD (`Task`)
into a fixed set of owned resources. These are a ServiceAccount, a
NetworkPolicy, an optional Role + RoleBinding, projected Secret volumes,
and a Job. Triggers (GitHub, Linear, slash commands, …) and agent images
are outside the controller and call it only through the Kubernetes API.

The repo is a kubebuilder v4 project with the single-group layout, the
domain `openconveyor.ai`, the module `github.com/openconveyor/openconveyor`,
and the projectName `conveyor`. It has three resources today: `Task`
(namespaced), `ClusterAgentClass`, and `ClusterTriggerClass` (both
cluster-scoped).

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

Tests use **Ginkgo + Gomega**. envtest provides a real apiserver +
etcd. The Makefile downloads the tooling (controller-gen, kustomize,
setup-envtest, golangci-lint) into `./bin/`. Do not install these tools
system-wide.

## Architecture rules that aren't obvious from the file tree

- **`internal/policy/` is pure.** It does no cluster I/O except DNS
  through the injectable `Resolver` interface. Keep new policy
  generators table-testable. Do not use a `client.Client` here.
- **`internal/controller/` owns I/O.** `task_controller.go` is the
  reconciler, and `build.go` assembles the Job and the SA. Resource
  builders always call `controllerutil.SetControllerReference` before
  they create a resource. Never write out-of-band, and never skip
  ownership.
- **The controller creates the NetworkPolicy before the Job.** Some
  CNIs have a default-allow window before they apply a policy. A
  different order breaks the security model. Do not change the ensure
  order in the reconciler.
- **`automountServiceAccountToken=false`** unless `permissions.rbac`
  is non-empty. Empty egress → a default-deny NetworkPolicy. The
  controller projects secrets as files at `/run/secrets/<name>/<key>`,
  never as env vars (ADR-0004). These defaults are guarantees, not
  settings.
- **`spec.resources.timeout` is mandatory** and maps to
  `Job.spec.activeDeadlineSeconds`. The Job has `backoffLimit: 0`.
  There are no retries on expensive agent runs.
- **The trigger adapter is a `manager.Runnable`** in the same binary.
  The flag `--trigger-bind-address` enables it. The webhook trust
  boundary is in `internal/trigger/signature.go` (HMAC). Read ADR-0006
  before you change it.
- **The shared label schema** is in `internal/policy/labels.go`. Use it
  for new owned resources so that `kubectl get … -l
  openconveyor.ai/task=<name>` continues to work.

## Conventions

- Use Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `test:`). Target `main`. One PR, one reason.
- Logging follows the K8s style guide (capital first letter, no
  trailing period, past tense, balanced key/value pairs). The
  `logcheck` linter enforces this rule. Do not disable it.
- After you edit `*_types.go` or kubebuilder markers, run
  `make manifests generate` and commit the diff. Do not hand-edit
  `zz_generated.*`, `config/crd/bases/*`, `config/rbac/role.yaml`,
  or `PROJECT`.
- If a change alters a security default, update
  `docs/security-model.md` in the same PR.
- Prose (Markdown, Go comments, sample and agent-image comments)
  follows Simplified Technical English. See `docs/writing-style.md`.
  `make lint-docs` checks it (warnings only).

## Agent skills

### Issue tracker

Issues are in GitHub Issues for `openconveyor/openconveyor` (through the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` at the repo root plus `docs/adr/`. See `docs/agents/domain.md`.
