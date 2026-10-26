---
name: Bug report
about: Report something that doesn't work as documented
title: "bug: "
labels: ["bug", "needs-triage"]
---

## What happened

<!-- Describe the actual behavior. Include the Task YAML if it is relevant. -->

## What you expected

<!-- Describe the expected behavior. -->

## Reproduction steps

1.
2.
3.

## Environment

- OpenConveyor version / git SHA:
- `kubectl version` (client + server):
- Cluster type (k3s / kind / EKS / …):
- Kubernetes version:
- CNI plugin (Calico / Cilium / Flannel / …):

## Logs

<!--
Paste the relevant controller logs:

  kubectl -n openconveyor-system logs deploy/openconveyor-controller-manager -c manager --tail=200

Add `kubectl describe task <name>` if the problem affects a Task.
-->

```
<logs here>
```

## Anything else

<!-- Stack traces, ADR references, and workarounds that you already tried. -->
