# Task status reference

What `Task.status` can say, and what each value means. Derived from
`api/v1alpha1/task_types.go` and the reason constants in
`internal/controller/status.go`; if they disagree, the code wins and
this page has a bug.

Triggers and dashboards should **watch `status.conditions`, not scrape
`status.phase`**: the phase is a coarse marker, the `Ready` condition
carries the reason and a human-readable message.

## Phases

`status.phase` is one of five values. The last three are **terminal**: a
terminal Task is never reconciled into a Job again (see
[`GLOSSARY.md`](../GLOSSARY.md), *Terminal Task*).

| Phase | Meaning | Terminal |
|---|---|---|
| `Pending` | Accepted; the Job has no running pod yet, is suspended, or the Task is waiting for its `ClusterAgentClass` | no |
| `Running` | The Job has an active pod or a start time | no |
| `Completed` | The Job reported `Complete` (or `SuccessCriteriaMet`) | yes |
| `Failed` | The Job reported `Failed` for a reason other than its deadline, or the spec could not be turned into a Job | yes |
| `TimedOut` | The Job reported `Failed` with reason `DeadlineExceeded`: `spec.resources.timeout` elapsed | yes |

An empty phase means the controller has not reconciled the Task yet.

### State machine

```text
            ┌──────────────────────────────────────┐
            │ (new Task)                           │
            ▼                                      │
        Pending ──────────────▶ Running            │
          │ ▲                     │                │
          │ └── Suspended Job ◀───┘                │
          │                       │                │
          │        ┌──────────────┼───────────────┐│
          ▼        ▼              ▼               ▼│
       Failed   Failed        Completed        TimedOut
   (InvalidSpec, (Job failed)                 (deadline)
    EgressResolveFailed)

   Terminal phases never transition again. With a TTL
   (spec.ttlSecondsAfterFinished or --task-ttl-seconds) the Task is
   deleted once it elapses.
```

`Pending` with reason `AgentClassMissing` is a wait state, not a
failure: the Task proceeds as soon as the referenced
`ClusterAgentClass` appears.

## The `Ready` condition

There is one condition type, `Ready`. Its `status` follows the phase:
`Unknown` while the Task is not terminal, `True` on `Completed`,
`False` on `Failed` and `TimedOut`. `reason` is a closed set:

| Reason | Phase | Status | Message | Set when |
|---|---|---|---|---|
| `AgentClassMissing` | `Pending` | `Unknown` | `Waiting for ClusterAgentClass "<ref>" to be created` | `spec.agent.ref` does not exist yet; re-checked on a watch and every 30 s |
| `Pending` | `Pending` | `Unknown` | `Waiting for pod to start` | The Job exists but has no start time or active pod |
| `Suspended` | `Pending` | `Unknown` | Job condition message, or `Job is suspended` | The Job carries `Suspended=True` |
| `Running` | `Running` | `Unknown` | `Job is running` | The Job has an active pod or a start time |
| `Completed` | `Completed` | `True` | Job condition message, or `Job completed` | The Job reported `Complete` / `SuccessCriteriaMet` |
| `Failed` | `Failed` | `False` | Job condition message, or `Job failed` | The Job reported `Failed` (not `DeadlineExceeded`) |
| `TimedOut` | `TimedOut` | `False` | Job condition message, or `Job exceeded its deadline` | The Job reported `Failed` with `DeadlineExceeded` |
| `InvalidSpec` | `Failed` | `False` | The validation or projection error | The spec passed admission but cannot become a Job: a prompt source that does not exist, `agent.config` that cannot be projected into the agent's declared inputs, a malformed resource quantity. Permanent; fix the spec and create a new Task |
| `EgressResolveFailed` | `Failed` | `False` | The lookup error, e.g. `egress entry "x": lookup failed: … no such host` | A hostname in the egress allowlist does not exist (NXDOMAIN). Permanent. Transient DNS errors (timeouts, SERVFAIL) are retried with backoff and do not touch the Task |

`observedGeneration` on the condition (and on `status`) is the
`metadata.generation` the controller reconciled against; a condition
whose `observedGeneration` is behind the object's generation describes
an older spec.

## Other status fields

| Field | Meaning |
|---|---|
| `jobName` | The owned Job (always the Task's own name) |
| `startTime` | Copied from the Job: when its first pod started |
| `completionTime` | Copied from the Job: when it finished, success or failure. Not set for `InvalidSpec` / `EgressResolveFailed`, which never ran a Job; the GC falls back to the `Ready` transition time |
| `observedGeneration` | Last `metadata.generation` reconciled |

## Events

Every transition is also published as a Kubernetes Event on the Task
(`kubectl describe task <name>`), with the condition reason as the
Event reason and the condition message as the Event message. `Normal`
for `Pending`, `Running`, `Completed`; `Warning` for `Failed`,
`TimedOut`, `InvalidSpec`, `EgressResolveFailed` and
`AgentClassMissing`. The two spec failures are prefixed in the Event
(`Task spec is invalid: …`, `Egress allowlist could not be
resolved: …`) because error text starts lowercase. Nothing is emitted
when a reconcile leaves the status unchanged.

## Consuming status

```sh
# Block until the Task is terminal (True = Completed, False = Failed/TimedOut).
kubectl wait task/<name> --for=condition=Ready --timeout=30m   # succeeds only on Completed
kubectl get task <name> -o jsonpath='{.status.phase}{"\t"}{.status.conditions[?(@.type=="Ready")].reason}{"\n"}'

# Everything that happened, in order.
kubectl describe task <name>
```

From code, read the `Ready` condition with
`meta.FindStatusCondition(task.Status.Conditions, "Ready")` and branch
on `Reason`. Treat `Status != Unknown` as final. Do not poll a terminal
Task: it will not change, and if it has a TTL it will disappear.

Prometheus: `conveyor_task_phase_transitions_total{phase,reason}`
counts the same transitions; see `internal/metrics/`.
