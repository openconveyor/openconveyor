# GitHub Issues → implementer agent

Label a GitHub issue with `conveyor:ready` and OpenConveyor opens a PR that
implements it.

## Flow

1. Someone labels an issue on `acme/widgets` with `conveyor:ready`.
2. GitHub POSTs to `https://<your-domain>/github/issues` with a
   `X-Hub-Signature-256` header.
3. The OpenConveyor trigger adapter validates the HMAC, filters to
   `action=labeled` + `label.name=conveyor:ready`, and creates a
   `Task` in the `conveyor-tasks` namespace with the issue body as
   the prompt.
4. The `claude-code-implementer` AgentClass runs. If the prompt asks
   for a code change, the agent clones the repo, edits, pushes a
   branch, and opens a PR.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first — namespace, AgentClass, API key Secrets.

### 1. Create the webhook signing Secret

```bash
# 32 bytes of entropy is the documented minimum; see ADR-0006.
SECRET=$(openssl rand -hex 32)
kubectl -n conveyor-system create secret generic github-webhook \
  --from-literal=secret="$SECRET"
```

Keep `$SECRET` handy — GitHub needs the same value.

### 2. Apply the ClusterTriggerClass

```bash
kubectl apply -f trigger.yaml
```

### 3. Configure GitHub

On the repository: **Settings → Webhooks → Add webhook**.

- **Payload URL:** `https://<your-domain>/github/issues`
- **Content type:** `application/json`
- **Secret:** the `$SECRET` from step 1
- **Events:** "Let me select individual events" → `Issues` only
- **Active:** ✓

### 4. Create the trigger label

```bash
gh label create conveyor:ready --repo acme/widgets \
  --color ededed \
  --description "Dispatch to OpenConveyor"
```

## Verify

```bash
# Create a test issue and label it.
gh issue create --repo acme/widgets --title "fix null deref in foo.go" \
  --body "foo.go:42 dereferences ptr without a nil check. Add one." \
  --label conveyor:ready

# Watch for the Task.
kubectl -n conveyor-tasks get tasks -w
```

Expected: within seconds, `kubectl get task` shows a `gh-issue-xxxxx`
Task transitioning through `Running` → `Completed`. If `kubectl get
tasks` is empty, check `kubectl -n conveyor-system logs
<conveyor-pod>` for signature errors.

## Security notes

- The webhook signing Secret in `conveyor-system` is the gate.
  Rotating it requires editing that Secret only — no OpenConveyor restart.
- `filters:` is doing real work. Without `label.name=conveyor:ready`
  every issue edit on your repo becomes an agent run. Do not remove
  the filters "just for testing."
- The Task's egress allowlist is the tightest form: `api.github.com`
  + `github.com`. No Anthropic endpoint listed — that comes from
  `ClusterAgentClass.spec.requires.egress`.
