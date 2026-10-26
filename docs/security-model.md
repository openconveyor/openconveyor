# Security Model

OpenConveyor's reason to exist is the difference between _"we documented
the safe way to run agents"_ and _"the API server refuses to give the
agent anything it did not explicitly ask for."_ Four hard guarantees,
all enforced by Kubernetes primitives, not by the agent image or by
docs.

## The four guarantees

### 1. Default-deny egress

Every Task gets a `NetworkPolicy` that applies
`PolicyTypes: [Ingress, Egress]` to its pod. When
`spec.permissions.egress` is empty, there are **zero egress rules** —
the pod cannot reach anything, not even DNS. When egress is declared,
DNS to `kube-system/k8s-app=kube-dns` on 53/udp+tcp is added, plus
the allowlisted CIDRs/hostnames on **port 443 only** (TCP and UDP, the
latter for HTTP/3). A Task that needs other ports lists them in
`spec.permissions.egressPorts`, which replaces the default for every
allowlist entry; the DNS rule keeps port 53 regardless. There is no
"all ports" setting. Ports are a separate field rather than a `:port`
suffix on entries because IPv6 CIDRs contain colons.

Hostnames are resolved to IPs at reconcile time. That is a snapshot,
and CDN/anycast records rotate, so while a Task with hostnames in its
allowlist is not terminal the controller re-resolves them every
`--egress-refresh-interval` (default `5m`, `0` disables) and updates
the NetworkPolicy when the set changed. Between refreshes the pod
keeps talking to the previous IPs; connections to an IP that left the
set are cut when the policy updates. For FQDN-level enforcement
install Cilium and swap the policy generator for
`CiliumNetworkPolicy` — that is the documented upgrade path.

See `internal/policy/networkpolicy.go` and the Phase 2 envtest in
`internal/controller/task_controller_test.go`.

### 2. Default-zero RBAC

`pod.spec.automountServiceAccountToken` is `false` unless the Task
declares `spec.permissions.rbac`. No ambient token in `/var/run` means
no accidental cluster API calls from a compromised agent. The Task's
ServiceAccount carries the same `automountServiceAccountToken` value,
so a pod that someone else points at that SA gets no token either; the
reconciler restores both if they are changed out-of-band.

When RBAC is declared, the reconciler materializes a namespaced
`Role` + `RoleBinding` containing only the verbs listed. The
ServiceAccount itself stays permission-less otherwise — the
`RoleBinding` is the only thing granting it anything.

Verify with `kubectl auth can-i --list --as=system:serviceaccount:<ns>:<task>`.

### 3. Declared secrets only

`spec.permissions.secrets` is a list of Secret names. The reconciler
projects each as a read-only volume at `/run/secrets/<name>/`, with one
file per Secret key: `/run/secrets/<name>/<key>`. Nothing is ever
exposed via env vars. The tradeoff:

- Env vars live in `/proc/<pid>/environ` and are inherited by every
  child process. A subprocess crashing to stderr with its env included
  will leak.
- Files have POSIX permissions and are explicit. The agent has to
  read them deliberately; they do not travel with `exec`.

Any Secret not listed is not mounted. `ls /run/secrets` in a zero-secret
Task is empty.

Because agents read secrets once at Job start, the values can rotate
freely underneath — which makes short-lived, externally-minted
credentials (e.g. GitHub App installation tokens refreshed by External
Secrets Operator) a drop-in upgrade over static PATs. See
[`enterprise-auth.md`](enterprise-auth.md).

### 4. Hard kill

`spec.resources.timeout` is required; the controller rejects any Task
without one with `reason=InvalidSpec`. The value maps to
`Job.spec.activeDeadlineSeconds`. A runaway agent burning tokens is
bounded in wall-clock.

Also: `backoffLimit: 0` — agent runs are expensive, never retry blindly.

## Pod hardening (belt and suspenders)

Every Job pod runs with, at **pod** level (`spec.securityContext`):

- `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`
- `seccompProfile.type: RuntimeDefault`
- `restartPolicy: Never`

and on the agent **container** (`containers[].securityContext`):

- `runAsNonRoot`, `runAsUser`, `runAsGroup` and `seccompProfile`
  repeated, so the container stays hardened even if the pod-level
  block is stripped by an admission mutation
- `readOnlyRootFilesystem: true` (plus an `emptyDir` at `/tmp`)
- `allowPrivilegeEscalation: false`
- `capabilities.drop: [ALL]`

Identity and seccomp sit on the pod because pod-level values also
apply to containers injected after the fact (service-mesh sidecars)
and are where the Pod Security Standards "restricted" profile reads
them. The remaining three fields exist only on containers. All of
this is set by the controller, independent of cluster-level Pod
Security admission. An agent image that refuses to start under these
is doing something we deliberately forbid.

## Controller permissions

The manager's own ClusterRole is generated from `+kubebuilder:rbac`
markers (`internal/controller/task_controller.go`,
`internal/trigger/handler.go`). Two entries deserve explanation:

- **`secrets` / `get`, cluster-wide.** Used only by the trigger
  adapter (ADR-0006) to read the HMAC key referenced by a
  `ClusterTriggerClass`, from the adapter's own namespace. The
  manager's client cache is disabled for Secrets (`cmd/main.go`), so
  the controller never opens a cluster-wide Secret watch and never
  holds Secrets in memory beyond a single lookup. There is no `list`
  or `watch` verb.
- **`clustertriggerclasses` / `get;list;watch`.** Route discovery for
  the webhook adapter; served from the informer cache.

## Threat model

**In scope.** A compromised agent image, or an agent whose prompt is
hostile, should not be able to:

- Touch secrets it did not declare.
- Call the k8s API beyond the verbs it declared.
- Talk to hosts off its egress allowlist.
- Exceed its declared wall-clock timeout.
- Persist state beyond the Job's lifetime (readonly rootfs; TTL-based
  Job cleanup).

**Out of scope.** OpenConveyor trusts the container runtime. A kernel
exploit, a containerd CVE, or an agent that owns the node directly
is not something NetworkPolicy can defend against. For untrusted
agent code, pair OpenConveyor with `RuntimeClass=gvisor` or `kata` at
the pod spec level, or swap the Job backend for `agent-sandbox`'s
sandboxed CRD once we add that as an alternative (deferred).

We also do not defend against a malicious user with `create tasks`
RBAC. Task authors are trusted to ask only for what they need;
auditing declared permissions is the cluster operator's job.

## Verification

From a Task pod (run `kubectl debug` or edit the image to include
`curl`/`sh`):

```sh
curl -v https://example.com                 # should fail: no egress
curl -v https://api.anthropic.com           # should work when declared
ls /run/secrets                             # should list only declared secrets
cat /var/run/secrets/kubernetes.io/serviceaccount/token  # should not exist
```

From outside the cluster:

```sh
kubectl auth can-i --list \
  --as=system:serviceaccount:<ns>:<task>    # only declared verbs

kubectl get networkpolicy <task> -o yaml    # deny-all ingress; declared egress only
```

The `envtest` suite covers the positive path (policy objects are
created correctly). The e2e suite (`make test-e2e`, kind) covers the
negative path: a Task with no egress whose agent runs
`wget https://example.com` must end `Failed` or `TimedOut`
(`test/e2e/testdata/task_egress_denied.yaml`).

One caveat the e2e spec works around rather than fixes: kind's CNI
(kindnet with kube-network-policies) only starts filtering a pod's
traffic once it has indexed the pod IP, about a second after the
container starts, and a connection opened inside that window is
accepted for its lifetime. The controller already creates the
NetworkPolicy before the Job, so nothing on its side can shrink the
window; it is a property of the CNI. CNIs that program policy at
sandbox creation (Calico, Cilium) do not have it. Pick accordingly
for production clusters.
