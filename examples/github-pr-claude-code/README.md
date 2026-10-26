# GitHub Pull Requests → reviewer agent

Label a GitHub pull request with `conveyor:review`, and OpenConveyor posts
a review comment that Claude Code generates.

The reviewer agent never pushes commits and never opens PRs. Its only side
effect on the git host is `gh pr review --comment`.

## Flow

1. Someone labels a PR on `acme/widgets` with `conveyor:review`.
2. GitHub POSTs to `https://<your-domain>/github/pull-request` with an
   `X-Hub-Signature-256` header.
3. The OpenConveyor trigger adapter validates the HMAC and filters to
   `action=labeled` + `label.name=conveyor:review`. It then creates a
   `Task` in the `conveyor-tasks` namespace with the URL of the PR as
   the prompt.
4. The `claude-code-reviewer` AgentClass runs. The agent fetches the diff
   with `gh pr diff` and gives the diff and the prompt to `claude --print`.
   It posts the result back with `gh pr review --comment`.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites)
first: the namespace, the AgentClass, and the API key Secrets.

### 1. Apply the reviewer ClusterAgentClass

```bash
kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass_reviewer.yaml
```

### 2. Create the webhook signing Secret

If you already configured the GitHub Issues example, the same `github-webhook`
Secret works. GitHub signs all webhook events of a repo with the same key.
Otherwise:

```bash
# 32 bytes of entropy is the documented minimum; see ADR-0006.
SECRET=$(openssl rand -hex 32)
kubectl -n conveyor-system create secret generic github-webhook \
  --from-literal=secret="$SECRET"
```

### 3. Apply the ClusterTriggerClass

```bash
kubectl apply -f trigger.yaml
```

### 4. Configure GitHub

On the repository: **Settings → Webhooks → Add webhook** (or extend the
existing webhook).

- **Payload URL:** `https://<your-domain>/github/pull-request`
- **Content type:** `application/json`
- **Secret:** the `$SECRET` from step 2
- **Events:** "Let me select individual events" → `Pull requests` only
- **Active:** ✓

### 5. Create the trigger label

```bash
gh label create conveyor:review --repo acme/widgets \
  --color ededed \
  --description "Dispatch to OpenConveyor reviewer"
```

## Verify

```bash
# On any open PR:
gh pr edit <number> --repo acme/widgets --add-label conveyor:review

# Watch for the Task.
kubectl -n conveyor-tasks get tasks -w
```

Expected result: within seconds, `kubectl get task` shows a `gh-pr-xxxxx`
Task that moves through `Running` → `Completed`. A new review comment
appears on the PR. If `kubectl get tasks` is empty, check the trigger
adapter logs for signature errors.

## Security notes

- **The token must not have write access.** Issue a fine-grained PAT
  with the scope of the target repo. Give it `Pull requests: Read and
  write` and no `Contents` access. The write permission is necessary to
  *post* a review. The agent has no `git push` path, but only the token
  prevents a later change from adding one.
- **`filters:` does real work.** Without `label.name=conveyor:review`,
  every label edit on every PR becomes an agent run. Do not remove the
  filters for a test.
- The egress allowlist of the Task is `api.github.com` + `github.com`.
  The Anthropic endpoint comes from `ClusterAgentClass.spec.requires.egress`.
- If you label a reviewed PR again, the trigger dispatches a second
  review. If this matters, add a gate: a workflow that removes the label
  after the first review. As an alternative, use a one-shot label
  (`conveyor:review-once`) that you delete before you apply it again.

## Customising the review

The default trigger maps `pull_request.html_url` into the prompt. To pass
a per-PR rubric, change the mapping to `pull_request.body`. The agent
still extracts the PR URL from the prompt with a regex, so any prompt
that contains the PR URL works.

To switch from "comment" to "approve" or "request changes" reviews,
override `REVIEW_EVENT` on the agent container. This requires a
customized AgentClass. The Task spec does not expose it today.
