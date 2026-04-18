# Security policy

OpenConveyor runs AI agents inside Kubernetes clusters with access to
secrets, network egress, and (optionally) the cluster API. The project
takes security reports seriously.

## Reporting a vulnerability

**Do not open a public GitHub issue for security problems.**

Please use GitHub's private vulnerability reporting:

1. Go to the repository's **Security** tab.
2. Click **Report a vulnerability**.
3. Describe the issue, the affected version / commit, reproduction
   steps, and impact.

Alternatively, email the maintainers listed in
[`MAINTAINERS.md`](MAINTAINERS.md).

## What to expect

- Acknowledgement within **72 hours** during alpha.
- An initial severity triage within **7 days**.
- A fix, mitigation, or formal accepted-risk decision before public
  disclosure.

## Supported versions

OpenConveyor is pre-1.0. Only `main` and the latest tagged release
receive security fixes. There is no LTS branch.

## Scope

In scope:

- The controller (`cmd/`, `internal/controller/`, `internal/policy/`).
- The webhook trigger adapter (`internal/trigger/`).
- The CRD schemas and generated RBAC (`api/v1alpha1/`, `config/`).
- Reference agent images under `agents/`.

Out of scope (please report upstream):

- Vulnerabilities in `sigs.k8s.io/controller-runtime`, `kubebuilder`,
  or the Kubernetes API server itself.
- Third-party agent images not shipped in this repository.

## Hardening assumptions

OpenConveyor is designed under the assumption that the cluster enforces
at least Pod Security Standards **baseline** at the namespace where
Tasks run. The guarantees in `docs/security-model.md` are layered on
top of that — they do not replace it.

Thanks for helping keep OpenConveyor secure.
