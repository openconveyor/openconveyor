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
the allowlisted CIDRs/hostnames (all TCP/UDP ports).

Hostnames are resolved to IPs at reconcile time. That is a snapshot:
if an allowlisted hostname's DNS record changes after the Task is
created, the pod keeps talking to the old IPs until the Task is
re-created. For FQDN-level enforcement install Cilium and swap the
policy generator for `CiliumNetworkPolicy` — that is the documented
upgrade path.

See `internal/policy/networkpolicy.go` and the Phase 2 envtest in
`internal/controller/task_controller_test.go`.

### 2. Default-zero RBAC

`pod.spec.automountServiceAccountToken` is `false` unless the Task
declares `spec.permissions.rbac`. No ambient token in `/var/run` means
no accidental cluster API calls from a compromised agent.

When RBAC is declared, the reconciler materializes a namespaced
`Role` + `RoleBinding` containing only the verbs listed. The
ServiceAccount itself stays permission-less otherwise — the
`RoleBinding` is the only thing granting it anything.

Verify with `kubectl auth can-i --list --as=system:serviceaccount:<ns>:<task>`.

### 3. Declared secrets only

`spec.permissions.secrets` is a list of Secret names. The reconciler
projects each as a read-only volume at `/run/secrets/<name>`. Nothing
is ever exposed via env vars. The tradeoff:

- Env vars live in `/proc/<pid>/environ` and are inherited by every
  child process. A subprocess crashing to stderr with its env included
  will leak.
- Files have POSIX permissions and are explicit. The agent has to
  read them deliberately; they do not travel with `exec`.

Any Secret not listed is not mounted. `ls /run/secrets` in a zero-secret
Task is empty.

### 4. Hard kill

`spec.resources.timeout` is required; the controller rejects any Task
without one with `reason=InvalidSpec`. The value maps to
`Job.spec.activeDeadlineSeconds`. A runaway agent burning tokens is
bounded in wall-clock.

Also: `backoffLimit: 0` — agent runs are expensive, never retry blindly.

## Pod hardening (belt and suspenders)

Every Job pod runs with:

- `runAsNonRoot: true`, `runAsUser: 65532`, `runAsGroup: 65532`
- `readOnlyRootFilesystem: true` (plus an `emptyDir` at `/tmp`)
- `allowPrivilegeEscalation: false`
- `capabilities.drop: [ALL]`
- `seccompProfile.type: RuntimeDefault`
- `restartPolicy: Never`

These are pod-level, independent of cluster-level Pod Security
Standards. An agent image that refuses to start under these is
doing something we deliberately forbid.

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
created correctly). A future CI job should add the negative path
(attempt a disallowed egress and assert it fails) against a real
kind cluster.
