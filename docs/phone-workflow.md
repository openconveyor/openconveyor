# Phone Workflow

OpenConveyor's GitHub Issues trigger works natively from your phone.
Create an issue on GitHub mobile, label it, and an agent runs
autonomously — cloning the repo, doing the work, and opening a PR.
You review the PR on your phone, merge, and dispatch the next task.

## Prerequisites

- The webhook adapter is reachable from the internet (tunnel, load
  balancer, or Cloudflare Tunnel).
- A GitHub webhook is configured on your repo pointing at the adapter
  (see `examples/github-issues-claude-code/README.md`).
- The `conveyor:ready` label exists on the repo.
- Secrets (`github-token`, `anthropic-api-key`) are created in the
  `conveyor-tasks` namespace.

## Step-by-step

### 1. Create an issue on your phone

Open the GitHub mobile app, navigate to your repo, and create a new
issue. The issue body is the prompt — write it like you would talk to
a developer:

> Set up a Go project with a basic HTTP server, Dockerfile, and
> Makefile. Use chi for routing. Open a PR against main.

### 2. Label the issue

Add the `conveyor:ready` label to the issue. This is the gate — the
webhook only fires when this specific label is applied, so drafting
an issue without the label is safe.

### 3. Agent runs

The webhook fires, the adapter creates a Task, and the controller
reconciles it into a hardened Job. The agent clones your repo, runs
Claude with your prompt, and opens a PR if it makes changes.

You'll get a GitHub notification on your phone when the PR is created.

### 4. Review and merge

Open the PR on GitHub mobile. Review the changes, leave comments, or
approve and merge. The agent's branch is deleted automatically after
merge (if you have that GitHub setting enabled).

### 5. Dispatch the next task

Create another issue with the next instruction:

> Add user authentication with JWT. Use the existing chi router.
> Include middleware and a /login endpoint. Open a PR against main.

Label it `conveyor:ready`. The cycle repeats.

## Iterative development workflow

This pattern works well for building a project incrementally:

1. **Scaffold** — "Set up the project structure with Go modules,
   Dockerfile, CI, and a basic health endpoint."
2. **Feature** — "Add a /users CRUD API with SQLite storage."
3. **Feature** — "Add input validation and error handling to the
   /users endpoints."
4. **Fix** — "The POST /users endpoint returns 500 when the email
   is duplicate. Add a unique constraint and return 409."
5. **Refactor** — "Extract the SQLite queries into a repository
   pattern."

Each step is one issue, one agent run, one PR. You review each PR
on your phone between steps.

## Tips

- **Be specific in the prompt.** Include file names, function names,
  and expected behavior. The agent works better with concrete
  instructions than vague requests.
- **Reference previous PRs.** "Building on the changes from PR #3,
  add pagination to the /users endpoint."
- **Set `TARGET_REPO` and `TARGET_BRANCH`.** The trigger template
  in `examples/github-issues-claude-code/trigger.yaml` maps
  `repository.full_name` into an annotation. The agent uses this
  to clone the right repo.
- **Check logs if the PR doesn't appear.** Find the Task name with
  `kubectl -n conveyor-tasks get tasks` and tail the Job logs:
  ```sh
  kubectl -n conveyor-tasks logs -f job/<task-name>
  ```

## Alternative: GitHub Actions workflow_dispatch

If your cluster is not reachable from the internet (no tunnel), you
can use GitHub Actions as the dispatch mechanism instead:

1. Create a workflow with `on: workflow_dispatch` and a `prompt`
   input field.
2. The workflow runs `kubectl apply` with the prompt embedded in a
   Task YAML.
3. This requires a self-hosted runner with cluster access, or a
   kubeconfig stored as a GitHub Secret.

This is not built as a reference example yet, but the pattern is
identical to the `claude-code-slash-dispatch` example — just
`kubectl apply` with a templated Task YAML.
