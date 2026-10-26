# ADR-0003: Triggers are out-of-process; anything that creates a Task CR is a trigger

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

The controller must accept work from many systems: issue trackers
(Linear, GitHub Issues, Plane), PR-opened webhooks, schedules, chat
commands, and local tools. We had to decide between two options. The
first option is a trigger plugin interface inside the OpenConveyor
binary: a Go interface, runtime-loadable `.so` files, or a gRPC plugin
protocol. The second option is to put all triggers out-of-process.

## Decision

A trigger is any component that can create a `Task` CR. There is no
in-process plugin interface, no plugin registry, and no dynamic
loading. OpenConveyor ships one _reference_ trigger, an HTTP webhook
adapter, and the `ClusterTriggerClass` CRD configures it. Everything
else that a user wants is YAML plus a process with the RBAC to create
Tasks.

This means:

- The webhook adapter is the only trigger inside the OpenConveyor
  binary, and it is generic: per-trigger behavior is data (the
  `ClusterTriggerClass` spec), not code.
- Cron-driven triggers are stock `CronJob`s that `kubectl apply` a
  Task YAML.
- A Claude Code slash command, a shell script, and a Discord bot are
  all valid triggers with zero OpenConveyor-side code.

## Alternatives considered

<!-- vale STE.Passive = NO -->
- **Go plugin interface** with `.so` files that the manager loads at
  startup. Rejected. Go `plugin` is fragile across module versions.
  With it, operators see cryptic "plugin was built with a different
  version" errors on every OpenConveyor upgrade.
<!-- vale STE.Passive = YES -->
- **gRPC plugin protocol** (HashiCorp-style). Rejected as
  overengineered for the actual use cases. Triggers are almost always
  "parse a webhook, build a Task." A CRD-driven generic adapter covers
  that; a gRPC surface would add a second way to do everything.
- **Single-binary "adapter per trigger type"** compiled in. Rejected.
  It forces every new trigger to ship a new binary release. That is
  how the OSC/"My Agent Tasks" project ended up Claude-only in
  practice: the activation energy for a new adapter is too high.

## Consequences

- **Zero activation energy for new triggers.** To add a trigger, you
  write something that can `kubectl apply` a Task. A slash command is
  one markdown file; a cron trigger is one YAML.
- **The webhook adapter has to be generic.** Per-trigger customization
  (signature validation, field mapping) has to be expressible in the
  `ClusterTriggerClass` CRD, not in code. That is a real constraint on
  the design of the adapter.
- **Debuggability is "kubectl".** An operator who wants to know why a
  trigger did not start runs `kubectl get tasks -w` and
  `kubectl describe <whatever emitted the Task>`. There is no unified
  "trigger dashboard" because there is no unified trigger runtime.
- **No shared middleware.** Each trigger solves cross-trigger concerns
  such as rate limiting, dedup, or auditing on its own. A later Task
  admission webhook, if we add one, is the other place for them.

## References

- `api/v1alpha1/clustertriggerclass_types.go`
