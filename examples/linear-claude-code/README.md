# Linear → implementer agent

Move a Linear ticket into a "Ready for Agent" workflow state and
OpenConveyor opens a PR.

## Flow

1. A human drags a ticket into the `Ready for Agent` column.
2. Linear POSTs to `https://<your-domain>/linear` with a
   `Linear-Signature` header.
3. The OpenConveyor trigger adapter validates the HMAC, filters to
   `type=Issue`, `action=update`, `data.state.name=Ready for Agent`,
   and creates a `Task` in the `conveyor-tasks` namespace with the
   ticket description as the prompt.
4. The `claude-code-implementer` AgentClass runs.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first — namespace, AgentClass, API key Secrets.

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

In your Linear team: **Settings → Workflow → Add state** named
`Ready for Agent` (exact match — the filter compares literally).

If you prefer a different state name, update `filters[2].equals`
in `trigger.yaml`.

## Verify

Move any issue into `Ready for Agent`. Then:

```bash
kubectl -n conveyor-tasks get tasks -w
```

A `linear-xxxxx` Task should appear within seconds, transition to
`Running`, and either `Completed` (PR opened against the repo the
ticket described) or `Failed` (logs in `kubectl logs`).

## Notes

- Linear fires `action=update` on *every* issue edit. The
  `data.state.name` filter is what narrows dispatch to the workflow
  transition. Without it you will get a Task every time someone
  tweaks the ticket.
- Linear payloads carry no repository, so the implementer's
  `$TARGET_REPO` cannot be mapped from the event. Point the trigger
  at a fixed repo by setting it statically on the template:
  ```yaml
  agent:
    ref: claude-code-implementer
    config:
      TARGET_REPO: https://github.com/your-org/your-repo.git
  ```
  Without it the agent runs prompt-only and skips the clone/PR step.
