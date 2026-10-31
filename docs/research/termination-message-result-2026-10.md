# Termination message as the Task result channel

Resolves wayfinder ticket #100 (map #98). Researched 2026-10-31 from the
Kubernetes `release-1.36` source tree, the kubernetes.io reference pages,
the controller-runtime v0.24.1 package docs, and this repo's code
(`internal/controller/build.go`, `task_controller.go`, `status.go`,
`docs/security-model.md`). Nothing below was run in a cluster. Items that
only a test can confirm are marked **unverified**.

## Verdict

The container termination message is a **sound result channel for the
success path**, and a **best-effort channel for the failure path**. It
is the only option in the comparison that costs the agent zero new
permissions. The agent writes a small JSON document to
`/dev/termination-log` before it exits. The kubelet copies the file
into `pod.status.containerStatuses[].state.terminated.message`. The
controller reads that field from the pod and copies validated fields
into `Task.status`.

What it costs the controller: one new RBAC rule (`pods` with
`get;list;watch`), one Pod informer limited by label selector, and a
second-level watch (Task → Job → Pod) that `Owns()` does not provide.
What it costs the security model: nothing on the agent side. All four
guarantees stay as they are. The one new risk is on the controller side.
The message is agent-controlled input, and the controller must treat it
as untrusted data before it writes anything into `Task.status`.

What it does not give: a result after a timeout. The Job controller
deletes the pod when `activeDeadlineSeconds` passes, and the pod object
is gone shortly after. An agent that traps `SIGTERM` can still write the
file inside the 30 s grace period, and a Pod watch can catch the final
status update. Treat that as a bonus, not a contract.

## Answers in short

| Question from #100 | Answer | Source |
|---|---|---|
| `terminationMessagePath` | Container field, default `/dev/termination-log`, immutable after creation. The kubelet creates a host file with mode `0666` and bind-mounts it at that path. A read-only root filesystem and UID 65532 do not block the write. | [core/v1 types](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go), [kuberuntime_container.go makeMounts](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go) |
| `terminationMessagePolicy` | `File` (default) reads the file on success and on failure. `FallbackToLogsOnError` reads the last 2048 bytes or 80 lines of the container log when the file is empty **and** the exit code is non-zero. Recommendation: set `File` explicitly. | same |
| 4096-byte limit | `MaxContainerTerminationMessageLength = 1024 * 4`. The kubelet keeps the **last** 4096 bytes of the file (`tail.ReadAtMost`), not the first. | [runtime.go](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/container/runtime.go), [tail.go](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/util/tail/tail.go) |
| Total pod limit | `MaxPodTerminationMessageLogLength = 1024 * 12`, divided equally over all containers of the pod (init, regular, ephemeral). The status manager cuts each message to that share, and it keeps the **first** bytes. With one agent container the per-container 4096 is the binding limit. The share drops under 4096 only at four or more containers. | [status_manager.go normalizeStatus](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/status/status_manager.go) |
| Where it surfaces | `pod.status.containerStatuses[].state.terminated.message`. With `restartPolicy: Never` the container never restarts, so `state` holds the final termination and `lastState` stays empty. The kubelet prefixes any runtime message with `": "`. | [core/v1 ContainerStatus](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go), [kuberuntime_container.go convertToKubeContainerStatus](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go) |
| Job → pod | The Job controller labels pods `batch.kubernetes.io/job-name=<job>` and `batch.kubernetes.io/controller-uid=<uid>`. This controller also puts `openconveyor.ai/task=<task>` on the pod template (`podLabels`). Either label finds the pod. | [batch/v1 types](https://github.com/kubernetes/api/blob/release-1.36/batch/v1/types.go), `internal/controller/build.go` |
| controller-runtime | `Owns()` enqueues only the direct controller owner, so a Pod event reaches the Job, not the Task. Use `Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(...))` keyed on the `openconveyor.ai/task` label, and restrict the Pod informer with `cache.Options.ByObject[&corev1.Pod{}].Label`. | [pkg/builder](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/builder), [pkg/handler](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/handler), [pkg/cache](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/cache) |
| RBAC | New marker: `+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch`. The manager's ClusterRole has no `pods` rule today. Then `make manifests` and the Helm chart sync. | `internal/controller/task_controller.go`, [RBAC docs](https://kubernetes.io/docs/reference/access-authn-authz/rbac/) |
| Race with GC and TTL | The Job gets `Complete` only after the Job controller counted the pod, and the pod stays until the Job's `ttlSecondsAfterFinished` cascade (default 3600 s here), PodGC (threshold 12500 terminated pods cluster-wide), or a manual delete. A Pod watch removes the dependency on the Job event. `--job-ttl-seconds=0` reopens the race. | [job_controller.go enactJobFinished](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go), [Job docs](https://kubernetes.io/docs/concepts/workloads/controllers/job/), [kube-controller-manager flags](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/) |
| Timeout | The Job controller deletes the active pod and adds `Failed/DeadlineExceeded`. The kubelet sends `SIGTERM`, waits `terminationGracePeriodSeconds` (default 30), then `SIGKILL`, then the pod object is removed. The message survives only if the agent writes it in the grace period and the controller catches the last pod update. | [job_controller.go](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go), [Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/) |
| `backoffLimit: 0` | One pod, no retry. A non-zero exit makes the pod `Failed` and the Job `Failed/BackoffLimitExceeded`. The Job controller deletes only **active** pods, so the failed pod and its message stay. The success and the failure path both deliver the message. | [job_controller.go deleteActivePods, canRemoveFinalizer](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go) |

## Comparison of the alternatives

| Channel | Agent needs | Controller needs | Survives Job TTL? | Result after timeout? | Security-model cost | Verdict |
|---|---|---|---|---|---|---|
| **Termination message** (`/dev/termination-log`) | Write one file. No token, no egress. | `pods` get/list/watch; a Pod watch with a label-restricted informer; input validation. | No. The pod goes with the Job. The controller must copy the result before that. | Best effort. Needs a `SIGTERM` trap in the agent and a Pod watch in the controller. | **Zero on the agent side.** Guarantees 1 to 4 unchanged. Controller side: one new read-only ClusterRole rule and one untrusted-input parser. | **Recommended.** |
| **Agent patches an annotation** on the Task (or on its own Pod) | A ServiceAccount token (`automountServiceAccountToken: true`), a Role with `tasks` `patch` (or `pods` `patch`), and egress to the API server. | Nothing new to read; it already watches Tasks. | Yes. The annotation lives on the Task. | Yes, if the agent patches before `SIGKILL`. | **Breaks guarantee 2** (default-zero RBAC): every Task that wants a result mounts a token. **Weakens guarantee 1**: the default-deny NetworkPolicy must allow the API server endpoint, on a port that the allowlist does not cover today. A compromised agent can rewrite its own Task. | Rejected. |
| **Result ConfigMap** that the agent creates or updates | Token, a Role with `configmaps` `create;update` (or `update` on one pre-created, Task-owned ConfigMap), and egress to the API server. | `configmaps` read (already granted); an owner reference so the ConfigMap is collected with the Task. | Yes, if the controller pre-creates the ConfigMap with an owner reference to the Task. | Yes, same condition as above. | Same as the annotation: **breaks guarantee 2, weakens guarantee 1.** `create` on `configmaps` also lets the agent write arbitrary ConfigMaps in the namespace. | Rejected. |
| **Scrape the container log** | Print a marker line. | `pods` get/list/watch **and** `pods/log` get; a client-go clientset (`PodExpansion.GetLogs`), because the controller-runtime client has no log method; a parser for unstructured text. | No. Same pod lifetime as the termination message, plus the kubelet rotates logs at `containerLogMaxSize` (default 10Mi) and `kubectl logs` returns only the latest file. | Same as the termination message. | Guarantees unchanged. Wider read scope for the controller (`pods/log` exposes every line, including secrets that a tool echoed). A larger parser surface on untrusted input. | Rejected. `FallbackToLogsOnError` is the bounded form of this and is also not recommended, see below. |
| **Job conditions only** (status quo) | Nothing. | Nothing. | n/a | n/a | None. | Gives no result. The baseline. |

## Details

### 1. The two container fields

The `core/v1` type doc for `terminationMessagePath` says: "Path at
which the file to which the container's termination message will be
written is mounted into the container's filesystem. Message written is
intended to be brief final status, such as an assertion failure
message. Will be truncated by the node if greater than 4096 bytes. The
total message length across all containers will be limited to 12kb.
Defaults to /dev/termination-log. Cannot be updated."
([types.go:3062-3070](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go)).

`terminationMessagePolicy`: "File will use the contents of
terminationMessagePath to populate the container status message on
both success and failure. FallbackToLogsOnError will use the last chunk
of container log output if the termination message file is empty and
the container exited with an error. The log output is limited to 2048
bytes or 80 lines, whichever is smaller. Defaults to File. Cannot be
updated." ([types.go:3071-3079](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go)).

Both fields are part of the container spec. `buildJob` in
`internal/controller/build.go` owns that spec, so the controller, not the
agent image, decides the path and the policy. The
`allowedEphemeralContainerFields` table in
`pkg/apis/core/validation/validation.go:110-111` marks both fields as
allowed even on ephemeral containers, so no admission rule in core
Kubernetes blocks them.

The kubelet does not need a writable root filesystem for the file.
`makeMounts` creates a file under the pod's container directory on the
host, sets its mode to `0666` "no matter what the umask is", and adds a
`runtimeapi.Mount` from that host path to the container path
([kuberuntime_container.go:505-537](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go)).
The agent container runs with `readOnlyRootFilesystem: true` and UID
65532, and the bind mount is a separate writable file. That the mode
`0666` file accepts a write from UID 65532 under the `RuntimeDefault`
seccomp profile is **unverified** by a run, but nothing in the hardening
list touches the mount. The kubernetes.io task page shows the same
pattern with a plain `echo ... > /dev/termination-log`
([Determine the Reason for Pod Failure](https://kubernetes.io/docs/tasks/debug/debug-application/determine-reason-pod-failure/)).

### 2. The size limits and which end survives

The constants are in
[`pkg/kubelet/container/runtime.go:806-820`](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/container/runtime.go):

```go
MaxPodTerminationMessageLogLength       = 1024 * 12
MaxContainerTerminationMessageLength    = 1024 * 4
MaxContainerTerminationMessageLogLength = 1024 * 2
MaxContainerTerminationMessageLogLines  = 80
```

Two truncation points apply in sequence, and they keep different ends.

1. **Per container, 4096 bytes, tail.** `getTerminationMessage` calls
   `tail.ReadAtMost(path, MaxContainerTerminationMessageLength)`
   ([kuberuntime_container.go:561](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go)).
   `ReadAtMost` "reads at most max bytes from the end of the file"
   ([tail.go:29-55](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/util/tail/tail.go)).
   A file over 4096 bytes loses its head.
2. **Per pod, 12 KiB shared, head.** `normalizeStatus` in the status
   manager computes `bytesPerStatus = 12288 / (containers +
   initContainers + ephemeralContainers)` and cuts each
   `Terminated.Message` to `Message[:bytesPerStatus]`
   ([status_manager.go:1306-1324](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/status/status_manager.go)).
   A file over the share loses its tail.

For this controller: the Job has one container, so the share is 12288
and the 4096 tail limit is the only one that matters. An injected
sidecar (two containers) gives 6144, still above 4096. The share falls
under 4096 only with four or more containers (3072 each). A result
under about 3 KiB is safe in every layout. The controller should hold
the agent to a smaller budget, for example 2048 bytes, so a stray
sidecar cannot change which bytes survive.

The kubelet limits the `FallbackToLogsOnError` path to 2048 bytes or 80 lines
(`readLastStringFromContainerLogs`,
[kuberuntime_container.go:573-582](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go)).

### 3. Where the message lands, and how the kubelet decorates it

`convertToKubeContainerStatus` runs only when the CRI reports
`CONTAINER_EXITED`. It reads the policy and path from the container
annotations, decides `fallbackToLogs` (policy is
`FallbackToLogsOnError` **and** exit code is non-zero **and** reason is
not `ContainerCannotRun`), and then appends:

```go
if len(tMessage) != 0 {
    if len(cStatus.Message) != 0 {
        cStatus.Message += ": "
    }
    cStatus.Message += tMessage
}
```

([kuberuntime_container.go:584-604](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go)).
Two consequences for a parser:

- A runtime message, when present, comes **first**, then `": "`, then
  the agent's text. The parser must find the start of the JSON object
  and must not assume byte zero.
- An unreadable file does not yield an empty message. It yields the
  literal `Error on reading termination log <path>: <err>`
  ([kuberuntime_container.go:566](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kuberuntime/kuberuntime_container.go)).
  The parser must reject this as "no result", not fail the Task.

The field is `ContainerStateTerminated.Message`: "Message regarding the
last termination of the container"
([types.go:3282-3303](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go)).
The same struct carries `exitCode`, `signal`, `reason`, `startedAt`,
`finishedAt`. `ContainerStatus.State` "holds details about the
container's current condition"; `LastTerminationState` "is not
populated if the container is still running and RestartCount is 0"
([types.go:3326-3333](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go)).
The Job pod has `restartPolicy: Never` and `backoffLimit: 0`, so the
container runs once and the controller reads
`state.terminated.message`. The kubernetes.io example uses
`lastState` because its demo pod keeps the default `restartPolicy:
Always` and restarts.

The kubelet writes the phase and the container statuses in one
`PodStatus` (`generateAPIPodStatus`,
[kubelet_pods.go:1901](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/kubelet/kubelet_pods.go)),
and `normalizeStatus` runs on that whole object. So the API server
never shows `phase: Succeeded` without the terminated container state
that produced it.

### 4. From the Job to the pod inside the reconciler

**Finding the pod.** The Job controller sets
`batch.kubernetes.io/job-name` and `batch.kubernetes.io/controller-uid`
on every pod ([batch/v1 types.go:45-51](https://github.com/kubernetes/api/blob/release-1.36/batch/v1/types.go);
[Job docs](https://kubernetes.io/docs/concepts/workloads/controllers/job/)).
This controller already puts `openconveyor.ai/task=<name>` on the pod
template (`podLabels` in `build.go`, `policy.PodSelectorLabels`). A
`List` with `client.InNamespace(ns)` and
`client.MatchingLabels{policy.LabelTaskName: task.Name}` returns the
pod. The `+kubebuilder:rbac` markers today grant nothing on `pods`
(`task_controller.go:66-75`), so the controller needs
`groups="",resources=pods,verbs=get;list;watch`, then `make manifests`
and the hand-synced Helm chart RBAC.

**Watching the pod.** `Owns(&batchv1.Job{})` is "the equivalent of
calling Watches(source.Kind(cache, &Type{},
handler.EnqueueRequestForOwner([...], &OwnerType{},
OnlyControllerOwner())))" and "reconciles only the first
controller-type OwnerReference of the given type"
([pkg/builder](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/builder)).
`EnqueueRequestForOwner` "enqueues Requests for the Owners of an
object", one level ([pkg/handler](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/handler)).
The pod's controller owner is the Job, not the Task, so `Owns(&corev1.Pod{})`
enqueues nothing useful. The fit is
`Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(fn))` where
`fn` reads the `openconveyor.ai/task` label and returns the Task key.
`tasksForAgentClass` in `task_controller.go` is the existing example of
this pattern.

**Cache cost.** The default cache "will watch and list requested
objects in all namespaces" ([pkg/manager Options](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/manager)).
A Pod informer without a selector holds every pod in the cluster.
`cache.Options.ByObject` "restricts the cache's ListWatch to the
desired fields per GVK at the specified object" and `ByObject.Label`
"represents a label selector for the object"
([pkg/cache](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/cache)).
Set `ByObject[&corev1.Pod{}] = {Label: <openconveyor.ai/task exists>}`
in `cmd/main.go` next to the existing `DisableFor: Secret` option. The
informer then holds only Task pods. `TransformStripManagedFields` as
`DefaultTransform` is a cheap extra. The alternative, a one-off read
through `mgr.GetAPIReader()`, "should be used sparingly and only when
the cached client does not fit your use case"
([pkg/cluster](https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/cluster));
it also gives no watch event, so the controller would depend on the
Job event alone.

**Ordering between the Job event and the pod.** The Job controller adds
`Complete` or `Failed` only after it counted the pod: `enactJobFinished`
returns false while `status.uncountedTerminatedPods` is non-empty
("Delaying marking the Job as finished, because there are still
uncounted pod(s)",
[job_controller.go:1636-1661](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go)).
Counting needs the pod in `Succeeded` or `Failed`, and section 3 shows
that the message is in the same status write. So when the Task
reconciler sees the Job condition, the API server already holds the
message. Two caches are still two informers. The Pod informer can lag
the Job informer by a watch event, so the reconciler must requeue when
the pod is absent or still `Running`, and the Pod watch delivers the
second wake-up.

**Lifetime of the pod.** "When a Job completes, no more Pods are
created, but the Pods are usually not deleted either"
([Job docs](https://kubernetes.io/docs/concepts/workloads/controllers/job/)).
The Job controller "removes the finalizer only after the Pod has been
accounted for in the Job status, allowing the Pod to be removed by
other controllers or users" (same page; the finalizer is
`batch.kubernetes.io/job-tracking`,
[batch/v1 types.go:35-44](https://github.com/kubernetes/api/blob/release-1.36/batch/v1/types.go)).
After that, three things can remove the pod:

1. The TTL-after-finished controller. "When the TTL controller cleans
   up the Job, it will delete the Job cascadingly, i.e. delete its
   dependent objects, such as Pods, together with the Job"
   ([Job docs](https://kubernetes.io/docs/concepts/workloads/controllers/job/);
   [TTL controller](https://kubernetes.io/docs/concepts/workloads/controllers/ttlafterfinished/)).
   This controller sets `ttlSecondsAfterFinished` from
   `--job-ttl-seconds`, default 3600 (`DefaultJobTTLSeconds`,
   `build.go`). The flag accepts `0`, and `0` means "eligible to be
   deleted immediately after it finishes"
   ([batch/v1 types.go:405-413](https://github.com/kubernetes/api/blob/release-1.36/batch/v1/types.go)).
   With `0` the pod can vanish before the reconciler runs.
2. PodGC. It "cleans up terminated Pods (with a phase of Succeeded or
   Failed), when the number of Pods exceeds the configured threshold"
   ([Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)).
   `--terminated-pod-gc-threshold int32 Default: 12500`
   ([kube-controller-manager](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/)).
   `gcTerminated` deletes the oldest terminated pods above the
   threshold, cluster-wide
   ([gc_controller.go:193-223](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/podgc/gc_controller.go)).
   On a busy shared cluster this can hit a finished Task pod within
   minutes. The controller does not own that number.
3. A human or another controller deletes the pod or the Job. A Task
   deletion cascades through the owner references the same way.

The Pod watch closes most of this window. The update that moves the pod
to `Succeeded` or `Failed` enqueues the Task at once, and the reconcile
copies the result in the same pass as the phase change. The remaining
window is the delay of one informer event plus one reconcile. When the
pod no longer exists and `Task.status` has no result, the controller records a
condition such as `Result=False, reason=ResultUnavailable`, and does
not retry forever. The controller should also reject
`--job-ttl-seconds=0` or document that it disables results.

### 5. Timeout and `backoffLimit: 0`

**Timeout.** When `pastActiveDeadline` is true, the Job controller sets
`finishedCondition` to `Failed/DeadlineExceeded` with "Job was active
longer than specified deadline", then "Remove active pods if Job
failed" calls `deleteActivePods`, which "issues deletion for active
Pods, preserving finalizers. This is done through DELETE calls that set
deletion timestamps"
([job_controller.go:1092-1139, 1248-1263](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go)).
The Job condition itself waits: "Can't declare the Job as finished yet,
as there might be remaining pod finalizers or pods that are not in the
informer's cache yet" (line 1133). On the node, "the kubelet triggers
the container runtime to send a TERM signal to process 1", waits the
grace period ("Defaults to 30 seconds",
[core/v1 types.go:4189-4198](https://github.com/kubernetes/api/blob/release-1.36/core/v1/types.go)),
then sends `SIGKILL`, and "the kubelet transitions the Pod into a
terminal phase (Failed or Succeeded depending on the end state of its
containers)" before "the API server deletes the Pod's API object"
([Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)).
`isPodFailed` then counts the deleted pod as failed
([job_controller.go:2183-2193](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go)).

So the sequence on timeout is: pod gets a `deletionTimestamp` → agent
gets `SIGTERM` → up to 30 s → terminal phase with container statuses →
pod object removed → Job `Failed/DeadlineExceeded`. By the time the
Task reconciler sees the Job condition, the pod is in most cases
no longer exists. Two things make a timeout result possible, and neither is
guaranteed:

- The agent traps `SIGTERM` and writes a partial result (for example
  "branch pushed, no PR") inside the grace period. The kubelet reads
  the file at `CONTAINER_EXITED`, which includes a kill.
- The controller watches pods. The status update with the terminal
  phase is a normal update event and arrives before the delete event
  (**unverified** that the informer always delivers it before the
  tombstone; a cache resync can collapse the two).

The honest contract: `TimedOut` means "no result expected"; a result
present after a timeout is extra information.

**`backoffLimit: 0`.** A non-zero exit sets the pod to `Failed`.
`pastBackoffLimitOnFailure` or `exceedsBackoffLimit` sets
`Failed/BackoffLimitExceeded` ("Job has reached the specified backoff
limit", line 1096). `deleteActivePods` only touches **active** pods, and
`canRemoveFinalizer` returns true for a finished Job, so the failed pod
loses its finalizer and stays
([job_controller.go:1488-1491](https://github.com/kubernetes/kubernetes/blob/release-1.36/pkg/controller/job/job_controller.go)).
The message and the exit code are both readable. This matters for the
agent contract: a failure result ("linter failed, see run X") is as
deliverable as a success result. The reconciler can also surface
`state.terminated.exitCode` and `reason` (`OOMKilled`, `Error`) in the
`Ready` condition, which `derivePhase` cannot do today from Job
conditions alone.

### 6. Security-model notes for the recommended channel

- **Guarantees 1 to 4 do not change.** The agent writes a file that
  the kubelet owns. No token, no egress, no secret, no timeout change.
- **New controller permission.** `pods` `get;list;watch` cluster-wide
  (the manager runs cluster-wide). Document it in the "Controller
  permissions" section of `docs/security-model.md` like the existing
  `secrets/get` entry. The label-restricted informer keeps the cache
  small but not the RBAC scope: a ClusterRole rule has no label
  selector.
- **The message is untrusted input.** A hostile prompt controls it.
  The controller must: cap the parsed length; find the first `{` and
  parse strict JSON with `DisallowUnknownFields`; accept a fixed set of
  keys; validate each URL (`https` only, a host that the Task's egress
  allowlist or a controller flag names, no credentials in the URL); and
  write only the validated fields to `Task.status`. Never copy the raw
  message into a condition message or an Event. `Task` is readable by
  more subjects than the pod, and a raw copy would turn `Task.status`
  into a leak path for a secret that the agent printed by accident.
- **Prefer `File` over `FallbackToLogsOnError`.** The fallback copies
  up to 2048 bytes of log into the pod status on a non-zero exit. Agent
  logs can hold tokens that a tool echoed. `pods` `get` is a more
  common grant than `pods/log` `get`, so the fallback widens who can
  read those bytes. Set `TerminationMessagePolicy:
  corev1.TerminationMessageReadFile` explicitly in `buildJob`, so a
  future default change cannot flip it.
- **Size discipline.** Ask agents for at most 2048 bytes. The kubelet
  keeps the tail at 4096 and the head at the pod share; a result that
  fits in both never gets cut.

### 7. Sketch of the change (for the implementation ticket)

Not part of this research, but the pieces are small:

- `api/v1alpha1/task_types.go`: a `TaskResult` struct on `TaskStatus`
  (for example `issueURL`, `pullRequestURL`, `summary` with a
  `MaxLength`), plus a `Result` condition with reasons
  `ResultRecorded`, `ResultInvalid`, `ResultUnavailable`. Then
  `make manifests generate` and the Helm chart CRD sync.
- `internal/controller/build.go`: set `TerminationMessagePath` and
  `TerminationMessagePolicy: File` on the agent container.
- `internal/controller/task_controller.go`: the `pods` RBAC marker, a
  `Watches(&corev1.Pod{}, ...)` with a map function on
  `policy.LabelTaskName`, and a result step in `syncStatus`.
- `internal/policy/`: a pure `ParseResult([]byte) (TaskResult, error)`
  with table tests. No client here.
- `cmd/main.go`: `cache.Options.ByObject` for Pods with the
  `openconveyor.ai/task` label selector.
- `agents/*/entrypoint.sh`: capture the `gh pr create` URL, write the
  JSON to `/dev/termination-log`, and add a `trap` on `TERM` that
  writes the partial result that the agent has at that point.
- `docs/security-model.md`, `docs/status-reference.md`, and a new ADR
  (the result channel is a trust-boundary decision like ADR-0004 and
  ADR-0006).

## Unverified items

- That a UID 65532 process writes to the `0666` bind-mounted file under
  `readOnlyRootFilesystem` and `RuntimeDefault` seccomp on containerd
  (expected yes; the kubelet designs the mount for exactly this).
- That the Pod informer delivers the terminal-phase update before the
  delete event on a timed-out pod in every case.
- Whether envtest can exercise the channel: envtest has no kubelet, so
  the unit path must fake the pod status; only `make test-e2e` on kind
  proves the end-to-end flow.
