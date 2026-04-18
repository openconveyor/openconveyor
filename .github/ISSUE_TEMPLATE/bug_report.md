---
name: Bug report
about: Report something that doesn't work as documented
title: "bug: "
labels: ["bug", "needs-triage"]
---

## What happened

<!-- Describe the actual behaviour. Include Task YAML if relevant. -->

## What you expected

<!-- Describe the expected behaviour. -->

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
Paste relevant controller logs:

  kubectl -n openconveyor-system logs deploy/openconveyor-controller-manager -c manager --tail=200

And `kubectl describe task <name>` if the Task is affected.
-->

```
<logs here>
```

## Anything else

<!-- Stack traces, ADR references, workarounds you've already tried. -->
