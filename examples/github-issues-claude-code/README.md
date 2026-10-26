# GitHub Issues → implementer agent

Label a GitHub issue with `conveyor:ready`, and OpenConveyor opens a PR
that implements the issue.

## Flow

1. Someone labels an issue on `acme/widgets` with `conveyor:ready`.
2. GitHub POSTs to `https://<your-domain>/github/issues` with an
   `X-Hub-Signature-256` header.
3. The OpenConveyor trigger adapter validates the HMAC and filters to
   `action=labeled` + `label.name=conveyor:ready`. It then creates a
   `Task` in the `conveyor-tasks` namespace with the issue body as
   the prompt.
4. The `claude-code-implementer` AgentClass runs. If the prompt asks
   for a code change, the agent clones the repo, edits the code, pushes
   a branch, and opens a PR.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first: the namespace, the AgentClass, and the API key Secrets.

### 1. Create the webhook signing Secret

```bash
# 32 bytes of entropy is the documented minimum; see ADR-0006.
SECRET=$(openssl rand -hex 32)
kubectl -n conveyor-system create secret generic github-webhook \
  --from-literal=secret="$SECRET"
```

Keep `$SECRET` available. GitHub needs the same value.

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

Expected result: within seconds, `kubectl get task` shows a
`gh-issue-xxxxx` Task that moves through `Running` → `Completed`. If
`kubectl get tasks` is empty, check `kubectl -n conveyor-system logs
<conveyor-pod>` for signature errors.

## Security notes

- The webhook signing Secret in `conveyor-system` is the gate.
  To rotate it, edit that Secret only. OpenConveyor does not need a restart.
- `filters:` does real work. Without `label.name=conveyor:ready`,
  every issue edit on your repo becomes an agent run. Do not remove
  the filters for a test.
- The egress allowlist of the Task is the tightest form: `api.github.com`
  + `github.com`. It lists no Anthropic endpoint. That endpoint comes
  from `ClusterAgentClass.spec.requires.egress`.
