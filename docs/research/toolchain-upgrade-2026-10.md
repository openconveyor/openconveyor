# Toolchain upgrade findings: Go 1.27 / controller-runtime 0.24 / k8s 1.36

Resolves wayfinder ticket #54 (map #53). Researched 2026-10-25 from upstream
release notes, upstream `go.mod` files, this repo's code, and the CI logs of
dependabot PRs #47 (go.mod bump) and #51 (Dockerfile bump). Go is not
installed on the research machine, so nothing below was compiled; items that
could only be confirmed by a build or lint run are marked **unverified**.

## Verdict

A kubebuilder scaffold re-sync is **optional, not required**. The dependency
bump (controller-runtime v0.23.3 -> v0.24.1, k8s.io/* v0.35.0 -> v0.36.3, Go
1.25 -> 1.27) compiles and lints with exactly three source edits plus the
go.mod/Dockerfile/devcontainer version lines: (1) replace the deprecated
`scheme.Builder` in `api/v1alpha1/groupversion_info.go:36` with
`runtime.NewSchemeBuilder` (controller-runtime #3461), (2) rewrite the three
`ptr.To(...)` calls at `internal/controller/build.go:127-129` as `new(...)`
because the `modernize/newexpr` linter fires once `go.mod` declares `go 1.26`,
and (3) bump the builder image to `golang:1.27` because the official image
sets `GOTOOLCHAIN=local` and refuses a `go 1.26.0` module. Every other change
in the four upstream release streams either does not touch an API this repo
calls or is a bug fix. The scaffold version that matches these exact targets
is kubebuilder v4.15.0; the current release, v4.16.0, already moved on to
controller-runtime v0.25.0 / k8s 0.37 and carries helm/v2-alpha breaking
changes (this repo uses that plugin), so a re-sync should be its own task
rather than part of the dep bump. Until then `PROJECT` keeps
`cliVersion: 4.13.1`, which is cosmetic.

## Tool pins

| Tool | Current pin (file) | Recommended pin | Reason / source |
|---|---|---|---|
| `go` directive | `go 1.25.3` (`go.mod:3`) | `go 1.26.0` | controller-runtime v0.24.1 `go.mod` declares `go 1.26.0` ([go.mod@v0.24.1](https://github.com/kubernetes-sigs/controller-runtime/blob/v0.24.1/go.mod)); kubebuilder v4.15.0 and v4.16.0 scaffolds use `go 1.26.0` ([testdata go.mod@v4.16.0](https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/testdata/project-v4/go.mod)). Dependabot PR #47 already writes `go 1.26.0`. |
| `toolchain` directive | `toolchain go1.25.12` (`go.mod:5`) | `toolchain go1.27.3` | Newest release; Go 1.27.3 / 1.26.10 shipped 2026-10-19 as a `[security]` release ([release history](https://go.dev/doc/devel/release), [golang-announce](https://groups.google.com/g/golang-announce)). Go 1.25 left support when 1.27.0 shipped on 2026-08-19 (last patch 1.25.14). `actions/setup-go` v6+ installs the `toolchain` version when present ([README](https://github.com/actions/setup-go/blob/main/README.md)). |
| Builder image | `golang:1.25` (`Dockerfile:2`, `.devcontainer/devcontainer.json:3`) | `golang:1.27` | PR #51 diff. The image sets `GOTOOLCHAIN=local` (visible in PR #47 docker-build log), so the image must be >= the `go` line. |
| sigs.k8s.io/controller-runtime | v0.23.3 | v0.24.1 | Ticket target. v0.24.1 = v0.24.0 + one cherry-pick (#3516, Apply typed-error regression) ([v0.24.1](https://github.com/kubernetes-sigs/controller-runtime/releases/tag/v0.24.1)). Note v0.25.x (k8s 1.37, new `EventRecorder` interface #3509) exists since 2026-09-03 and is out of scope ([v0.25.0](https://github.com/kubernetes-sigs/controller-runtime/releases/tag/v0.25.0)). |
| k8s.io/{api,apimachinery,client-go} | v0.35.0 | v0.36.3 (PR #47) | controller-runtime v0.24.1 requires >= v0.36.0. Newest 1.36 patch is v1.36.5 (2026-09-23, [releases](https://github.com/kubernetes/kubernetes/releases)); either is fine, PR #47 pins v0.36.3. |
| controller-tools (`CONTROLLER_TOOLS_VERSION`, `Makefile:191`) | v0.20.1 | v0.21.0 | Built against k8s.io/* v0.36.0 and Go 1.26.0, ships envtest v1.36.0; kubebuilder v4.15.0 pin ([v0.21.0](https://github.com/kubernetes-sigs/controller-tools/releases/tag/v0.21.0)). v0.22.0 (kubebuilder v4.16.0 pin) is also safe: it deprecates the long-form `+kubebuilder:validation:Required/Optional` markers, which this repo does not use (`api/` uses `+optional`/`+required`). Avoid v0.23.0 for now: it is a deliberate breaking release (#1474 starts honouring `+k8s:maxItems/minimum/maximum`) and bumps to controller-runtime v0.25.0 ([v0.23.0](https://github.com/kubernetes-sigs/controller-tools/releases/tag/v0.23.0)). Any of v0.21+ needs a Go >= 1.26 toolchain to `go install`. |
| kustomize (`KUSTOMIZE_VERSION`, `Makefile:190`) | v5.8.1 | keep v5.8.1 | kubebuilder v4.16.0 still pins v5.8.1. v5.8.2/v5.8.3 state "No functional changes in kustomize"; v5.8.3 requires `go 1.27.0` to install ([releases](https://github.com/kubernetes-sigs/kustomize/releases), [go.mod@v5.8.3](https://github.com/kubernetes-sigs/kustomize/blob/kustomize/v5.8.3/kustomize/go.mod)). |
| golangci-lint (`GOLANGCI_LINT_VERSION`, `Makefile:203`; `.custom-gcl.yml:6`) | v2.8.0 | v2.15.0 (minimum v2.13.0) | v2.13.0 (2026-08-19) is the first release with "go1.27 support" (#6642) and `go 1.26.0` in its go.mod ([v2.13.0](https://github.com/golangci/golangci-lint/releases/tag/v2.13.0)). kubebuilder v4.16.0 pins v2.13.1. v2.15.0 (2026-10-16) is current, x/tools v0.52.0, and includes "fix: compute custom version hash with module versions" for `golangci-lint custom` ([releases](https://github.com/golangci/golangci-lint/releases)). Both files must carry the same version. |
| sigs.k8s.io/logtools (`.custom-gcl.yml:13`) | v0.10.1 | keep v0.10.1 (v0.11.0 optional) | Both tags declare `go 1.23.0` and `golang.org/x/tools v0.26.0` in their own go.mod ([go.mod@v0.10.1](https://github.com/kubernetes-sigs/logtools/blob/v0.10.1/go.mod), [go.mod@v0.11.0](https://github.com/kubernetes-sigs/logtools/blob/v0.11.0/go.mod)); the module-plugin build resolves x/tools to golangci-lint's version, so neither tag constrains Go support. v0.11.0 (2026-10-16) adds a `//logcheck:wrapper` marker and auto-detects wrapper aliases ([PR #37](https://github.com/kubernetes-sigs/logtools/pull/37)); whether it produces new findings here is **unverified**. |
| kind (`KIND_VERSION`, `.github/workflows/test-e2e.yml:14`, `test-chart.yml:14`) | v0.32.0 | keep v0.32.0 for this bump | v0.32.0's default node image is `kindest/node:v1.36.1`, i.e. already k8s 1.36, and `Makefile:81` creates the cluster without `--image`. v0.33.0 (2026-08-26) defaults to v1.37.0 and ships `kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed`; bumping it means adding `--image` to stay on 1.36 ([kind releases](https://github.com/kubernetes-sigs/kind/releases)). |
| envtest (`ENVTEST_VERSION`, `ENVTEST_K8S_VERSION`, derived in `Makefile:194-202`) | release-0.23 / 1.35 | release-0.24 / 1.36 (derived automatically) | `release-0.24` branch and `tools/setup-envtest/v0.24.{0,1}` tags exist. `setup-envtest use 1.36` means `1.36.*` and resolves to the newest index entry, v1.36.2 ([setup-envtest help](https://github.com/kubernetes-sigs/controller-runtime/blob/release-0.24/tools/setup-envtest/main.go), [envtest-releases.yaml](https://raw.githubusercontent.com/kubernetes-sigs/controller-tools/HEAD/envtest-releases.yaml)). |
| actions/setup-go | v7 (`go-version-file: go.mod`) | unchanged | Reads the `toolchain` directive, so CI runs go1.27.3 once the line is set. |

## Recommended Go version

`go.mod`:

```
go 1.26.0

toolchain go1.27.3
```

`Dockerfile:2`: `FROM --platform=$BUILDPLATFORM golang:1.27 AS builder`.
`.devcontainer/devcontainer.json:3`: `"image": "golang:1.27"`.

Why this split: `go 1.26.0` is the minimum controller-runtime v0.24.1 and the
kubebuilder scaffold demand, keeps the module loadable by any Go >= 1.26 (the
`golang:1.27` image included, since it runs `GOTOOLCHAIN=local` and only
checks the `go` line; that the `toolchain` line is not enforced under `local`
is an inference from [go.dev/doc/toolchain](https://go.dev/doc/toolchain),
**unverified** by a build). `toolchain go1.27.3` makes `actions/setup-go`,
`go run govulncheck` and local `make test` use the newest security release.
Declaring `go 1.27.0` instead is also acceptable (kustomize v5.8.3 does it);
nothing in this repo needs 1.27 language features, so there is no gain.

govulncheck: PR #51's run (2026-08-24) reported six stdlib advisories
(GO-2026-6218, 6091, 6090, 6089, 5972, 5026) all "Fixed in: ...@go1.25.13";
PR #47's run on go1.26.0 reported GO-2026-5856 (fixed in go1.26.5) and
GO-2026-5039 (fixed in go1.26.4). go1.27.3 post-dates every fixed-in version
above, so the job is expected to pass; whether any advisory published after
2026-10-19 affects go1.27.3 is **unverified** until CI runs.

## Breaking changes that touch this repo

### 1. `scheme.Builder` deprecated (controller-runtime #3461)

Where: `api/v1alpha1/groupversion_info.go:24` (import of
`sigs.k8s.io/controller-runtime/pkg/scheme`) and `:36`
(`SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}`).
PR #47 lint log: `SA1019: scheme.Builder is deprecated: This helper is only
useful in api packages, but api packages should be easy to import and hence
have minimal dependencies. (staticcheck)`.

Change (this is the kubebuilder >= v4.15.0 scaffold, see
[groupversion_info.go@v4.16.0](https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/testdata/project-v4/api/v1/groupversion_info.go)):

```go
import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

SchemeBuilder = runtime.NewSchemeBuilder(func(scheme *runtime.Scheme) error {
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
})
AddToScheme = SchemeBuilder.AddToScheme
```

`runtime.SchemeBuilder.Register` takes `func(*runtime.Scheme) error`, not
objects, so the three `init()` registrations must change too
(`api/v1alpha1/task_types.go:210`, `clusteragentclass_types.go:131`,
`clustertriggerclass_types.go:177`), following
[captain_types.go@v4.16.0](https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/testdata/project-v4/api/v1/captain_types.go):

```go
func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Task{}, &TaskList{})
		return nil
	})
}
```

After this `api/v1alpha1` no longer imports controller-runtime at all, which
is the stated goal of #3461. Run `make manifests generate` afterwards; the
`zz_generated.deepcopy.go` output should be unchanged (**unverified**).

### 2. `modernize/newexpr` fires once `go.mod` says `go 1.26`

Where: `internal/controller/build.go:127`, `:128`, `:129`
(`ptr.To(true)`, `ptr.To(nonRootUID)` x2). PR #47 lint log:
`newexpr: call of To(x) can be simplified to new(x) (modernize)`. Go 1.26
lets `new` take an expression ([Go 1.26 notes](https://go.dev/doc/go1.26));
controller-runtime (#3489) and the kubebuilder scaffold (#5775) made the same
switch. `make lint-fix` should rewrite them; the remaining `ptr.To` calls at
`build.go:130-164` and `internal/policy/networkpolicy.go:205-219` were not
flagged in that run, so leave them or convert for consistency (**unverified**
whether a newer golangci-lint flags more of them).

### 3. Builder image too old for the module

Where: `Dockerfile:2` (`golang:1.25`). PR #47 docker-build and e2e
`BeforeSuite` logs: `go: go.mod requires go >= 1.26.0 (running go 1.25.12;
GOTOOLCHAIN=local)`. Fix: `golang:1.27` (PR #51). The two PRs must land
together or be squashed into one; each fails CI alone.

### 4. Go 1.25 toolchain is end-of-life and fails govulncheck

Where: `go.mod:5` (`toolchain go1.25.12`), `.github/workflows/test.yml:54`.
See "Recommended Go version" above.

### 5. New transitive modules in go.sum (no code change)

PR #47 adds `k8s.io/streaming v0.36.3` (the `httpstream` packages moved out of
apimachinery, kubernetes #137298) and `github.com/cenkalti/backoff/v5`
(controller-runtime v0.24.0 dependency change). Neither is imported by this
repo; `go mod tidy -diff` in CI is the only place they matter.

## Changes that do NOT affect this repo

controller-runtime v0.24.0 ([notes](https://github.com/kubernetes-sigs/controller-runtime/releases/tag/v0.24.0)):

- The only item marked breaking is the k8s.io/* v0.36 bump.
- `ctrl.Options`, `client.Options{Cache: &client.CacheOptions{DisableFor}}`,
  `metricsserver.Options{BindAddress, SecureServing, TLSOpts, FilterProvider,
  CertDir, CertName, KeyName}`, `webhook.Options{TLSOpts, CertDir, CertName,
  KeyName}`, `webhook.NewServer`, `filters.WithAuthenticationAndAuthorization`,
  `healthz.Ping`, `mgr.Add`, `mgr.GetFieldIndexer().IndexField`,
  `controllerutil.SetControllerReference`, `reconcile.Result{RequeueAfter}`
  (all used in `cmd/main.go` and `internal/controller/`): no signature or
  field changes in the release notes.
- #3465 removed a deprecated webhook custom-path function: this project has no
  webhooks (`PROJECT` lists none; `cmd/main.go` only constructs the server).
- #3445 `IndexField` no longer blocks on informer sync: the repo calls it in
  `SetupWithManager` (`task_controller.go:359`) before `mgr.Start`, so timing
  is unchanged.
- Fake-client fixes (#3443, #3431, #3430, #3498) are bug fixes; the repo's
  uses (`task_controller_test.go:813`, `handler_test.go:42`) only use
  `WithScheme`/`WithObjects`/`Build`.
- #3447 envtest now stops the whole process group: `suite_test.go:92-94` still
  wraps `testEnv.Stop()` in `Eventually`, which remains valid.
- The new `EventRecorder` interface and k8s 1.37 are v0.25.0, out of scope.

Kubernetes 1.36 ([CHANGELOG-1.36](https://github.com/kubernetes/kubernetes/blob/master/CHANGELOG/CHANGELOG-1.36.md)):

- Deprecations: `Service.spec.externalIPs`, direct access to
  `metav1.FieldsV1.Raw`, `git-repo` volume plugin, credential-plugin
  `AllowlistEntry.Name`: none referenced here (grep for `FieldsV1`,
  `sets.String`, `NewSimpleClientset` is empty).
- batch/v1 Job: items are controller-side (gang scheduling, stale-cache
  deferral, `startTime` error text); the fields this repo sets
  (`backoffLimit`, `activeDeadlineSeconds`, `ttlSecondsAfterFinished`,
  `automountServiceAccountToken`, projected volumes) are unchanged.
- networking/v1 NetworkPolicy: no API changes listed.
- client-go: informer/reflector/leader-election changes are internal to
  controller-runtime's cache.
- Kubernetes 1.36.0 itself was built with Go 1.26.2, consistent with the
  `go 1.26.0` floor.

Go 1.26 ([notes](https://go.dev/doc/go1.26)):

- `ServeMux` trailing-slash redirects now 307: `internal/trigger/server.go:52-55`
  registers `/healthz` and `/` only; no trailing-slash patterns.
- `crypto/*` ignores custom random sources, TLS hybrid KEX defaults, cookie
  scoping: not exercised by this code.
- `go mod init` default version change: irrelevant to an existing module.

Go 1.27 ([notes](https://go.dev/doc/go1.27)):

- `encoding/json` is backed by the v2 implementation with "behavior
  preserved, but the exact text of error messages may differ". The repo
  decodes JSON in `internal/policy/config.go:164`,
  `internal/trigger/mapping.go:137,144`, `handler.go`, and tests; no test
  asserts on a JSON error string (grep for `invalid character`,
  `unexpected end of JSON`, `cannot unmarshal` is empty). Escape hatch if
  something surfaces: `GOEXPERIMENT=nojsonv2`.
- TLS GODEBUGs (`tls10server`, `tlsrsakex`, `tls3des`, `x509keypairleaf`,
  `tlsunsafeekm`) are now permanent: `cmd/main.go:125-127` only sets
  `NextProtos`; no code lowers `MinVersion`.
- `gofmt` alignment change may re-wrap whitespace: run `make fmt` once under
  1.27 so `test.yml`'s `git diff --exit-code` after `make test` stays clean
  (**unverified** whether any file changes).
- `go test` now runs the `stdversion` vet check; `go vet printf` flags
  `%w` with a pointer-to-error-type: `make vet` will say if either bites
  (**unverified**).
- `Response.Body` auto-drain, HTTP/2 client priority, `go mod tidy` require
  block merging (only for `go 1.27` modules), `bzr` removal: no effect.

Other tools:

- controller-tools v0.22.0 marker deprecation: repo uses only `+optional`,
  `+required`, `+kubebuilder:validation:Enum`, `+kubebuilder:validation:XValidation`.
  v0.23.0 #1474 affects `+k8s:maxItems/minimum/maximum`, which the repo
  does not use.
- Ginkgo v2.27.2 -> v2.32.0 and Gomega v1.38.2 -> v1.40.0: no entries marked
  breaking; minimum Go raised to 1.24 ([ginkgo CHANGELOG](https://github.com/onsi/ginkgo/blob/master/CHANGELOG.md),
  [gomega CHANGELOG](https://github.com/onsi/gomega/blob/master/CHANGELOG.md)).
- kind v0.32.0 notes: kubeadm v1beta4 config for 1.36+ and Envoy replacing
  HAProxy affect only custom kind configs; the Makefile uses none.
- kustomize v5.8.2/v5.8.3: no functional changes.

## kubebuilder go/v4 scaffold: v4.13.1 -> v4.16.0

Per-release facts (from [v4.14.0](https://github.com/kubernetes-sigs/kubebuilder/releases/tag/v4.14.0),
[v4.15.0](https://github.com/kubernetes-sigs/kubebuilder/releases/tag/v4.15.0),
[v4.16.0](https://github.com/kubernetes-sigs/kubebuilder/releases/tag/v4.16.0)
and the `testdata/project-v4` tree at each tag):

| Scaffold | go | controller-runtime | k8s.io/* | controller-tools | kustomize | golangci-lint | notes |
|---|---|---|---|---|---|---|---|
| v4.13.1 (this repo) | 1.25.3 | v0.23.3 | v0.35.0 | v0.20.1 | v5.8.1 | v2.8.0 | `scheme.Builder`, `ptr.To`, `golang:1.25` |
| v4.14.0 | 1.25.7 | v0.23.3 | v0.35 | v0.20.1 | v5.8.1 | v2.11.4 | helm RBAC rework (`rbacHelpers` -> `rbac.helpers`), many helm values added |
| v4.15.0 | 1.26.0 | v0.24.1 | v0.36.0 | v0.21.0 | v5.8.1 | v2.12.2 | `runtime.NewSchemeBuilder`, helm `enable` -> `enabled`, `depguard` bans `sort`, `golang:1.26`, helm NetworkPolicy templates |
| v4.16.0 | 1.26.0 | v0.25.0 | v0.37.0 | v0.22.0 | v5.8.1 | v2.13.1 | `new(expr)` instead of `ptr.To`, `ARG BASE_IMAGE=golang:1.26`, `AfterSuite` no longer retries `testEnv.Stop()`, webhook renames, helm `manager.healthProbe.port` required, `serviceAccount.name` required when disabled, `manager.args` via `tpl`, cert-manager v1.21.1, e2e on kind 1.37.0 |

Re-sync assessment:

- Required for the dep bump: **no**. Sections 1-3 above are the full set of
  code changes and they are copied from the scaffold by hand.
- If a re-sync is wanted later, v4.15.0 is the one whose pins equal the
  ticket's targets. v4.16.0 is the current CLI and brings controller-runtime
  v0.25.0 / k8s 0.37 / kind 1.37 plus helm/v2-alpha breaking changes that
  this repo's `dist/chart` consumers would feel; `kubebuilder alpha update`
  does a three-way merge from `cliVersion` in `PROJECT`.
- Optional scaffold-aligned nits, each independent of the bump:
  `test/utils/utils.go:31` cert-manager v1.20.0 -> v1.21.1;
  `internal/controller/suite_test.go:92-94` drop the `Eventually` around
  `testEnv.Stop()` (#5838); `.golangci.yml` add the `depguard` `sort` rule.

## ENVTEST / kind pairs for Kubernetes 1.36

| Component | 1.36 options | Source |
|---|---|---|
| envtest binaries (`setup-envtest use 1.36`) | `v1.36.0`, `v1.36.2` (newest; `1.36` selects it); `v1.37.0` also indexed | [envtest-releases.yaml](https://raw.githubusercontent.com/kubernetes-sigs/controller-tools/HEAD/envtest-releases.yaml), [controller-tools releases](https://github.com/kubernetes-sigs/controller-tools/releases) |
| setup-envtest tool (`ENVTEST_VERSION`) | `release-0.24` branch (derived from controller-runtime v0.24.1); tags `tools/setup-envtest/v0.24.0`, `v0.24.1` | controller-runtime git refs |
| kind v0.32.0 | default `kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5` | [kind v0.32.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.32.0) |
| kind v0.33.0 | `kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed` (default is v1.37.0, so pass `--image`) | [kind v0.33.0](https://github.com/kubernetes-sigs/kind/releases/tag/v0.33.0) |

The Makefile derives both envtest values from `go.mod`, so bumping the
modules is enough; no Makefile edit is needed for envtest.

## Files to touch for the bump (checklist)

- `go.mod` / `go.sum`: PR #47 diff plus `toolchain go1.27.3`.
- `Dockerfile:2`, `.devcontainer/devcontainer.json:3`: `golang:1.27`.
- `api/v1alpha1/groupversion_info.go`, `task_types.go:210`,
  `clusteragentclass_types.go:131`, `clustertriggerclass_types.go:177`:
  section 1.
- `internal/controller/build.go:127-129`: section 2.
- `Makefile:191` `CONTROLLER_TOOLS_VERSION ?= v0.21.0`; `Makefile:203` and
  `.custom-gcl.yml:6` `v2.15.0`.
- Then `make manifests generate fmt lint test`.
