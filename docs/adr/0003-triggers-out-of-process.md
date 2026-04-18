# ADR-0003: Triggers are out-of-process; anything that creates a Task CR is a trigger

- **Status:** Accepted
- **Date:** 2026-04-17
- **Deciders:** Martijn Scholten

## Context

The controller needs to support arbitrary sources of work: issue
trackers (Linear, GitHub Issues, Plane), PR-opened webhooks,
schedules, chat commands, local tools. We had to decide whether to
define a trigger plugin interface inside the OpenConveyor binary (Go
interface, runtime-loadable .so files, or a gRPC plugin protocol),
or to push triggers entirely out-of-process.

## Decision

A trigger is any component that can create a `Task` CR. There is
no in-process plugin interface, no plugin registry, no dynamic
loading. OpenConveyor ships one _reference_ trigger — an HTTP webhook
adapter — configured via the `ClusterTriggerClass` CRD. Everything
else that a user might want is just YAML + a process with RBAC to
create Tasks.

This means:

- The webhook adapter is the only trigger that lives inside the
  OpenConveyor binary, and it is generic: per-trigger behavior is data
  (`ClusterTriggerClass` spec), not code.
- Cron-driven triggers are stock `CronJob`s that `kubectl apply` a
  Task YAML.
- A Claude Code slash command, a shell script, a Discord bot — all
  are valid triggers with zero OpenConveyor-side code.

## Alternatives considered

- **Go plugin interface** with `.so` files loaded at manager
  startup. Rejected. Go `plugin` is notoriously fragile across
  module versions; operators would hit cryptic "plugin was built
  with a different version" errors on every OpenConveyor upgrade.
- **gRPC plugin protocol** (HashiCorp-style). Rejected as
  overengineered for the actual use cases — triggers are almost
  always "parse a webhook, build a Task." A CRD-driven generic
  adapter covers that; a gRPC surface would add a second way to
  do everything.
- **Single-binary "adapter per trigger type"** compiled in. Rejected.
  Forces every new trigger to ship a new binary release, which is
  how the OSC/"My Agent Tasks" project ended up Claude-only in
  practice — the activation energy for a new adapter is too high.

## Consequences

- **Zero activation energy for new triggers.** Adding a trigger is
  literally "write something that can `kubectl apply` a Task." A
  slash command is one markdown file; a cron trigger is one YAML.
- **The webhook adapter has to be genuinely generic.** Per-trigger
  customization (signature validation, field mapping) has to be
  expressible in the `ClusterTriggerClass` CRD, not in code. That
  is a real constraint on the adapter's design.
- **Debuggability is "kubectl".** An operator watching for why a
  trigger didn't fire runs `kubectl get tasks -w` and
  `kubectl describe <whatever emitted the Task>`. There is no
  unified "trigger dashboard" because there is no unified trigger
  runtime.
- **No shared middleware.** Cross-trigger concerns like rate
  limiting, dedup, or auditing have to be solved per-trigger (or in
  the Task admission webhook, should we add one later).

## References

- Plan: section "Plugin Interfaces" in
  `/home/martijn/.claude/plans/hazy-shimmying-umbrella.md`
- `api/v1alpha1/clustertriggerclass_types.go`
