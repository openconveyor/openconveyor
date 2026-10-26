# Linear → implementer agent

Move a Linear ticket into the "Ready for Agent" workflow state, and
OpenConveyor opens a PR.

## Flow

1. A human drags a ticket into the `Ready for Agent` column.
2. Linear POSTs to `https://<your-domain>/linear` with a
   `Linear-Signature` header.
3. The OpenConveyor trigger adapter validates the HMAC and filters to
   `type=Issue`, `action=update`, `data.state.name=Ready for Agent`.
   It then creates a `Task` in the `conveyor-tasks` namespace with the
   ticket description as the prompt.
4. The `claude-code-implementer` AgentClass runs.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first: the namespace, the AgentClass, and the API key Secrets.

### 1. Create the webhook signing Secret

```bash
SECRET=$(openssl rand -hex 32)
kubectl -n conveyor-system create secret generic linear-webhook \
  --from-literal=secret="$SECRET"
```

### 2. Apply the ClusterTriggerClass

```bash
kubectl apply -f trigger.yaml
```

### 3. Configure Linear

**Settings → API → Webhooks → New webhook**.

- **URL:** `https://<your-domain>/linear`
- **Resource types:** `Issues`
- **Secret:** the `$SECRET` from step 1
- **Enabled:** ✓

### 4. Create the workflow state

In your Linear team: **Settings → Workflow → Add state** with the name
`Ready for Agent`. Use the exact name, because the filter compares the
literal string.

If you prefer a different state name, update `filters[2].equals`
in `trigger.yaml`.

## Verify

Move any issue into `Ready for Agent`. Then:

```bash
kubectl -n conveyor-tasks get tasks -w
```

A `linear-xxxxx` Task appears within seconds and moves to `Running`.
It then becomes `Completed` (the agent opened a PR against the repo
that the ticket described) or `Failed` (read the logs with `kubectl logs`).

## Notes

- Linear sends `action=update` on *every* issue edit. The
  `data.state.name` filter narrows the dispatch to the workflow
  transition. Without it, you get a Task each time someone changes
  the ticket.
- Linear payloads carry no repository, so the trigger cannot map the
  `$TARGET_REPO` of the implementer from the event. Point the trigger
  at a fixed repo. Set the repo statically on the template:
  ```yaml
  agent:
    ref: claude-code-implementer
    config:
      TARGET_REPO: https://github.com/your-org/your-repo.git
  ```
  Without it, the agent runs prompt-only and skips the clone and PR step.
