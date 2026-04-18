# `/conveyor` — Claude Code slash command as a trigger

Run `/conveyor please refactor the auth package to use context.Context`
inside a Claude Code session and OpenConveyor runs that prompt as an
autonomous Job on your cluster. No webhook, no HMAC, no server.

This example is the sharpest proof of OpenConveyor's trigger-pluggability
thesis: the trigger is a markdown file on your laptop and `kubectl`.

## How it works

1. `commands/conveyor.md` is a Claude Code project-level slash command.
2. When you type `/conveyor <prompt>`, Claude Code interpolates
   `$ARGUMENTS` and runs the embedded `!kubectl apply -f -` heredoc.
3. The heredoc is a `Task` CR that points at the `claude-code-implementer`
   AgentClass with a canned permissions block.
4. The Task reconciler materialises a hardened Job that runs the agent
   with your prompt as input.

No OpenConveyor-side code ships for this to work — triggers are out of
process (see [ADR-0003](../../docs/adr/0003-triggers-out-of-process.md)).

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first — the `conveyor-tasks` namespace, AgentClass, and API key
Secrets must exist.

### Install the slash command (project-local)

```bash
mkdir -p .claude/commands
ln -s "$(pwd)/examples/claude-code-slash-dispatch/commands/conveyor.md" \
    .claude/commands/conveyor.md
```

This makes `/conveyor` available only inside the cloned OpenConveyor
repo. For a global install, symlink into `~/.claude/commands/` instead.

### Verify your kubeconfig points at the cluster

The slash command inherits whatever `kubectl` does. Make sure the
current context is the homelab (or whatever cluster is running
OpenConveyor):

```bash
kubectl config current-context
kubectl -n conveyor-tasks get clusteragentclass claude-code-implementer
```

If the second command returns `NotFound`, run the shared-prerequisites
setup first.

## Usage

Inside a Claude Code session:

```
/conveyor fix the null-check on line 42 of foo.go and open a PR
```

Expected output: the created Task's name and a `kubectl logs -f` tail
command you can run in another terminal.

Tail the run:

```bash
kubectl -n conveyor-tasks logs -f job/<task-name>
```

When the Job completes, `kubectl get task <task-name>` will show
`phase: Completed` (or `Failed` — the logs explain why).

## Customising

- **Tighter permissions.** Fork `task-template.yaml` or edit the
  heredoc in `commands/conveyor.md`. A read-only reporter variant
  should drop `github-token`, drop `api.github.com`, and point at a
  different AgentClass.
- **Different cluster per project.** Each Claude Code project has its
  own `.claude/commands/`. Keep a project-local `conveyor.md` that
  pins `kubectl --context=<name>` if you bounce between clusters.
- **Prompt context.** The heredoc appends hostname + branch + cwd to
  `$ARGUMENTS` so the agent knows where the request came from.
  Extend this block with anything else worth snapshotting (last
  commit SHA, TODO comments, …).

## Out of scope for v1

- **MCP server variant.** A small local MCP server that exposes
  `dispatch_task` as a tool — so the model itself can decide when to
  hand off work — is the logical next step once this example proves
  the UX. Not shipping until then.
- **Status polling / Telegram notification.** The run happens
  detached; reconnecting is a separate concern (Phase 9's reporter
  path is the right place to wire that).
