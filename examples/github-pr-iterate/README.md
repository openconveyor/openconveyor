# GitHub Pull Requests → implementer in iterate mode

Label a pull request with `conveyor:iterate`, and the implementer
addresses the review on the same branch. This is an **Iteration**: it
pushes to the branch of the PR and never opens a second PR.

## Flow

1. A reviewer (a human or the reviewer agent) leaves review threads or
   comments on a PR that the implementer opened.
2. Someone labels the PR `conveyor:iterate`.
3. GitHub POSTs to `https://<your-domain>/github/pull-request-iterate`.
   The adapter validates the HMAC, filters to `action=labeled` +
   `label.name=conveyor:iterate`, and checks that no iteration for the
   same PR is active. Then it creates a Task with `PR_NUMBER`,
   `TARGET_REPO` and `TARGET_BRANCH`.
4. The implementer reads the PR, the linked issue, the unresolved review
   threads and the comments and reviews since the last push. It checks
   out the PR branch with `gh pr checkout` and lets Claude edit the
   code. Then it commits and pushes to the same branch.
5. It replies `Addressed in <sha>.` on each thread that Claude
   addressed. It does not resolve the threads: you do that after you
   check the change.
6. It removes `conveyor:iterate`, so that you can apply it again for
   the next round. It removes and adds `conveyor:review`, so that the
   reviewer trigger starts a new review.
7. The Result says `Addressed <n> of <m> review threads on PR #<pr> in
   <sha>` and links the PR and the issue.

The implementer cannot push to a PR from a fork, and the Task then
fails. A PR without unresolved threads and without new comments ends
the Task at once: the implementer removes `conveyor:iterate` and
changes nothing.

## Setup

Read the [shared prerequisites](../README.md#shared-prerequisites) and
[GitHub labels and webhooks](../README.md#github-labels-and-webhooks)
first. This example uses the implementer AgentClass and the same
`github-webhook` signing Secret as the other GitHub examples.

### 1. Apply the ClusterTriggerClass

```bash
kubectl apply -f trigger.yaml
```

### 2. Configure GitHub

On the repository: **Settings → Webhooks → Add webhook**. This is a
third webhook: the reviewer class already has `/github/pull-request`,
and the adapter serves one class for each path.

- **Payload URL:** `https://<your-domain>/github/pull-request-iterate`
- **Content type:** `application/json`
- **Secret:** the value of the `github-webhook` Secret, without a
  trailing newline
- **Events:** "Let me select individual events" → `Pull requests` only
- **Active:** ✓

### 3. Create the labels

Create `conveyor:ready`, `conveyor:review` and `conveyor:iterate` as
[GitHub labels and webhooks](../README.md#github-labels-and-webhooks)
shows.

## Verify

```bash
# Leave a review comment on a line of the PR in the GitHub UI, then:
gh pr edit <number> --repo acme/widgets --add-label conveyor:iterate

kubectl -n conveyor-tasks get tasks -w
```

Expected result: a `gh-iterate-xxxxx` Task moves through `Running` →
`Completed`. The PR has a new commit, the thread has the reply
`Addressed in <sha>.`, and the PR no longer has `conveyor:iterate`.

```bash
kubectl -n conveyor-tasks get task <name> -o jsonpath='{.status.result.summary}{"\n"}'
```

## Security notes

- The token needs `Contents: Read and write` (push) and `Pull requests:
  Read and write` (replies, labels). The PAT of the Issues example has
  both.
- `filters:` does real work. Without `label.name=conveyor:iterate`,
  every label change on every PR starts an iteration.
- Every write of the implementer arrives at the adapter as a new
  webhook event. Only a `labeled` event with `conveyor:iterate` starts
  an iteration, and the implementer only removes that label. Thus the
  loop never starts itself.
