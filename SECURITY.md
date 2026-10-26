# Security policy

OpenConveyor runs AI agents inside Kubernetes clusters. The agents have
access to secrets, network egress, and optionally the cluster API. The
project takes security reports seriously.

## Reporting a vulnerability

**Do not open a public GitHub issue for security problems.**

Use the private vulnerability reporting of GitHub:

1. Go to the **Security** tab of the repository.
2. Click **Report a vulnerability**.
3. Describe the issue, the affected version or commit, the reproduction
   steps, and the impact.

As an alternative, send an email to the maintainers in
[`MAINTAINERS.md`](MAINTAINERS.md).

## What to expect

- We acknowledge the report within **72 hours** during alpha.
- We complete an initial severity triage within **7 days**.
- We provide a fix, a mitigation, or a formal accepted-risk decision
  before public disclosure.

## Supported versions

OpenConveyor is pre-1.0. Only `main` and the latest tagged release
receive security fixes. There is no LTS branch.

## Scope

In scope:

- The controller (`cmd/`, `internal/controller/`, `internal/policy/`).
- The webhook trigger adapter (`internal/trigger/`).
- The CRD schemas and generated RBAC (`api/v1alpha1/`, `config/`).
- Reference agent images under `agents/`.

Out of scope (report these upstream):

- Vulnerabilities in `sigs.k8s.io/controller-runtime`, `kubebuilder`,
  or the Kubernetes API server itself.
- Third-party agent images that this repository does not ship.

## Hardening assumptions

The design of OpenConveyor assumes that the cluster enforces at least
Pod Security Standards **baseline** in the namespace where Tasks run.
The guarantees in `docs/security-model.md` add to that enforcement.
They do not replace it.

Thank you for your help to keep OpenConveyor secure.
