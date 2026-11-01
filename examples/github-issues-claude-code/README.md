# GitHub Issues → implementer agent

Label a GitHub issue with `conveyor:ready`, and OpenConveyor opens a PR
that implements the issue.

## Flow

1. Someone labels an issue on `acme/widgets` with `conveyor:ready`.
2. GitHub POSTs to `https://<your-domain>/github/issues` with an
   `X-Hub-Signature-256` header.
3. The OpenConveyor trigger adapter validates the HMAC and filters to
   `action=labeled` + `label.name=conveyor:ready`. When no Task for the
   same issue is active, it creates a `Task` in the `conveyor-tasks`
   namespace with `ISSUE_NUMBER`, `TARGET_REPO` and `TARGET_BRANCH`.
4. The `claude-code-implementer` AgentClass runs. The agent reads the
   issue with `gh`, clones the repo, and lets Claude edit the code. Then
   it pushes `conveyor/issue-<n>` and opens a PR with `Closes #<n>`. It
   comments on the issue with the PR link, and labels the PR
   `conveyor:review`.
5. `kubectl get task` shows the PR URL in `status.result`.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first: the namespace, the AgentClass, and the API key Secrets. Then read
[GitHub labels and webhooks](../README.md#github-labels-and-webhooks):
the three labels, one webhook for each path, and the newline pitfall of
the webhook secret.

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

### 4. Create the labels

Create `conveyor:ready`, `conveyor:review` and `conveyor:iterate` as
[GitHub labels and webhooks](../README.md#github-labels-and-webhooks)
shows. The implementer adds `conveyor:review` to its PR.

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
`gh-issue-xxxxx` Task that moves through `Running` → `Completed`. Then
the PR URL is in the Result:

```bash
kubectl -n conveyor-tasks get task -o wide   # the Summary column
kubectl -n conveyor-tasks get task <name> \
  -o jsonpath='{.status.result.links[?(@.name=="pull-request")].url}{"\n"}'
``` If
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
