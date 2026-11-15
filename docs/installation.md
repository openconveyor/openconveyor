# Installation

The [quick start](../README.md#quick-start) installs the controller
with one `kubectl apply`. This page gives the other install paths and
the controller settings.

You need a Kubernetes 1.29+ cluster. The CEL validation rules on the
CRDs require version 1.29.

## Helm

The release workflow publishes the chart as an OCI artifact and
attaches it to each release as a `.tgz`. The chart installs the CRDs
and keeps them on uninstall unless you set `crd.keep=false`.

```sh
helm install conveyor oci://ghcr.io/openconveyor/charts/conveyor \
  --version 0.4.0 \
  --namespace conveyor-system --create-namespace
```

## From source

`make install` applies the CRDs and `make run` runs the controller
against your current kubeconfig. To deploy your own build, set `IMG`
and run `make docker-build docker-push deploy`.

## Webhook triggers

Start the manager with `--trigger-bind-address=:9090` to enable the
adapter. It reads `ClusterTriggerClass` objects and their signing
Secrets from the namespace given by `--trigger-namespace` (default
`conveyor-system`).
[`plugin-authoring.md`](plugin-authoring.md) describes the
`ClusterTriggerClass` fields.

## Controller flags

| Flag | Default | Helm value | What it does |
|---|---|---|---|
| `--trigger-bind-address` | blank (disabled) | none | Starts the webhook trigger adapter on this address. Use it with `--trigger-namespace`, the namespace that holds the HMAC Secrets. |
| `--egress-refresh-interval` | `5m` | none | Re-resolves the egress hostnames while a Task is not terminal, so the NetworkPolicy follows DNS changes. `0` disables the refresh. |
| `--job-ttl-seconds` | `3600` | `manager.jobTTLSeconds` | Kubernetes keeps a completed Job and its logs for this many seconds. The minimum is `1`. The controller refuses `0`, because the pod can then disappear before the controller reads the Result. |
| `--task-ttl-seconds` | unset | `manager.taskTTLSeconds` | The controller deletes a Terminal Task after this many seconds. The Task's own `spec.ttlSecondsAfterFinished` wins. Unset at both levels keeps the Task. |
