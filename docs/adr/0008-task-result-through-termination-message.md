# ADR-0008: Agents report a Result through the container termination message

- **Status:** Accepted
- **Date:** 2026-10-31
- **Deciders:** Martijn Scholten

## Context

An agent that opens a pull request or writes an issue produces one
small fact that other systems need: the URL of what it made. Today the
Task status records only the phase and the Job conditions. A trigger
that waits for the outcome (for example a chat bot that must answer
"here is your PR") has to poll the external system. We need a channel
from the agent to the controller that does not weaken the four
guarantees in `docs/security-model.md`. Decided in
[wayfinder ticket #103](https://github.com/openconveyor/openconveyor/issues/103)
on the research in
[#100](https://github.com/openconveyor/openconveyor/issues/100).

## Decision

The agent writes its Result as strict JSON to the container
termination message file (`/dev/termination-log`). The controller
reads it from `pod.status.containerStatuses[].state.terminated.message`
and stores the validated fields on the Task.

- **Shape.** `status.result` holds a `summary` (plain text, at most
  512 characters, control characters removed) and `links`, a list of
  at most 8 `{name, url}` entries. The file has the same shape. The
  Task API version is the version of this contract; the file carries no
  version field. Conventional link names are `issue` and
  `pull-request`; an agent can add others.
- **Size.** The agent writes at most 2048 bytes. The controller sets
  `terminationMessagePolicy: File` explicitly in the Job spec and never
  uses `FallbackToLogsOnError`.
- **Validation.** The message is untrusted input. The controller caps
  the length, finds the first `{`, parses strict JSON with unknown
  fields rejected, and accepts a link only when its URL is `https` and
  its host is in the effective egress allowlist of the Task (Task
  permissions plus the ClusterAgentClass requirements). A rejected
  link is dropped. The controller never copies the raw message into a
  condition, an Event or a log line.
- **Status.** A `Result` condition reports `Recorded`, `NotProvided`,
  `Unavailable` (the pod was gone before the controller read it) or
  `Invalid`. The Result never changes the phase. A Completed Task and a
  Failed Task can both carry one. A TimedOut Task carries one only as a
  bonus: the agent can write a partial Result in the SIGTERM grace
  period, and the controller records it when it catches the last pod
  update.
- **Controller side.** The manager gets `pods` `get;list;watch`, a Pod
  watch limited to the `openconveyor.ai/task` label, and refuses
  `--job-ttl-seconds=0` at startup because that value can delete the
  pod before the controller reads the message.

## Alternatives considered

- **The agent patches an annotation on the Task or on its own pod.**
  Rejected. The agent then needs a ServiceAccount token, a Role and
  egress to the API server. That breaks guarantee 2 (default-zero RBAC)
  for every Task that wants a Result, and a compromised agent can
  rewrite its own Task.
- **The agent writes a result ConfigMap.** Rejected for the same reason,
  and `create` on ConfigMaps lets the agent write arbitrary objects in
  the namespace.
- **The controller scrapes the container log.** Rejected. It needs
  `pods/log`, a client-go clientset and a parser for unstructured text
  that can contain secrets a tool echoed.
- **`FallbackToLogsOnError`.** Rejected. It copies up to 2048 bytes of
  log into the pod status on a non-zero exit, where more subjects can
  read it than can read the log.
- **A free `outputs` map or a fixed struct with `issueURL` and
  `pullRequestURL`.** Rejected. The map is hard to validate; the fixed
  struct needs an API change for every new kind of agent.

## Consequences

- Guarantees 1 to 4 do not change. The agent needs no token, no egress
  and no new mount to report a Result.
- The manager ClusterRole gains a cluster-wide read on pods.
  `docs/security-model.md` lists it under controller permissions.
- A Result is lost when the pod disappears before the controller reads
  it (PodGC on a busy cluster, a manual delete). The `Result` condition
  then says `Unavailable`. This is a known gap, not a bug.
- Triggers and bots that wait for an outcome watch `Task.status`, not
  the external system.
- The reference agents write the file on success, on failure, and on
  SIGTERM where they can.

## References

- Research: branch `research/termination-message-result`,
  `docs/research/termination-message-result-2026-10.md`.
- `internal/controller/build.go` (Job spec), `internal/controller/status.go`
  (conditions), `docs/security-model.md`, `docs/status-reference.md`.
- ADR-0003 (triggers out of process), ADR-0004 (secrets as files).
