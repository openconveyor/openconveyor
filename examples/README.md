# Examples

Six ways to start agents. They prove the trigger-pluggability thesis
of OpenConveyor. The reconciler, the AgentClass, and the Task shape are
identical across the examples. Only the trigger differs.

| Example | Trigger | What it shows |
|---|---|---|
| [`linear-claude-code/`](linear-claude-code/) | Linear webhook | SaaS issue tracker → Task |
| [`github-issues-claude-code/`](github-issues-claude-code/) | GitHub Issues webhook | Git-host issue tracker → Task |
| [`github-pr-claude-code/`](github-pr-claude-code/) | GitHub Pull Requests webhook | PR labeled → review comment (reviewer archetype) |
| [`github-pr-iterate/`](github-pr-iterate/) | GitHub Pull Requests webhook | PR labeled → the implementer addresses the review (iterate mode) |
| [`claude-code-slash-dispatch/`](claude-code-slash-dispatch/) | Claude Code slash command (laptop) | Zero-infra, zero-webhook trigger |
| [`cronjob-reporter/`](cronjob-reporter/) | Kubernetes CronJob | Scheduled Task dispatch (reporter archetype) |

## Shared prerequisites

Every example assumes:

1. **You installed OpenConveyor.** See the top-level [`README.md`](../README.md).
   This directory assumes that the CRDs and the controller manager are running.
2. **A Task namespace exists.** The samples target `conveyor-tasks`:

   ```bash
   kubectl create namespace conveyor-tasks
   ```

3. **You applied the AgentClass for the example.** The AgentClass is
   cluster-scoped, one per archetype. The Linear, GitHub Issues, PR
   iterate and slash-command examples use the implementer. The PR example uses the
   reviewer. The CronJob example uses the reporter.

   ```bash
   kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass.yaml           # implementer
   kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass_reviewer.yaml  # reviewer
   kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass_reporter.yaml  # reporter
   ```

4. **Runtime secrets exist in `conveyor-tasks`.** Adjust them per example.
   The key name inside each Secret does not matter to the reference
   agents. The agents export the file under the name of the Secret, for
   example `ANTHROPIC_API_KEY`. All examples use these Secrets:

   ```bash
   kubectl -n conveyor-tasks create secret generic anthropic-api-key \
     --from-literal=key="$ANTHROPIC_API_KEY"
   kubectl -n conveyor-tasks create secret generic github-token \
     --from-literal=token="$GITHUB_TOKEN"
   ```

5. **Webhook examples only:** the trigger adapter is running. Start
   `conveyor` with `--trigger-bind-address=:9090`. The webhook signing
   Secret is in the namespace of the adapter (default `conveyor-system`).

## GitHub labels and webhooks

The three GitHub examples form one loop. Label an issue `conveyor:ready`,
and the implementer opens a PR with the label `conveyor:review`. That
label starts the reviewer. Label the PR `conveyor:iterate`, and the
implementer addresses the review on the same branch.

1. **Create the three labels on the repository.** The implementer adds
   and removes `conveyor:review` and `conveyor:iterate`, and GitHub
   rejects a label that does not exist.

   ```bash
   gh label create conveyor:ready   --repo acme/widgets --color 0e8a16 --description "Dispatch to OpenConveyor"
   gh label create conveyor:review  --repo acme/widgets --color 1d76db --description "OpenConveyor reviews this PR"
   gh label create conveyor:iterate --repo acme/widgets --color fbca04 --description "OpenConveyor addresses the review"
   ```

2. **Add one webhook for each path.** GitHub sends each webhook to one
   URL, and the adapter serves one `ClusterTriggerClass` for each path.

   | Payload URL path | Events | ClusterTriggerClass |
   |---|---|---|
   | `/github/issues` | Issues | `github-issues-implementer` |
   | `/github/pull-request` | Pull requests | `github-pr-reviewer` |
   | `/github/pull-request-iterate` | Pull requests | `github-pr-iterate` |

3. **Keep the webhook secret free of a trailing newline.**
   `openssl rand -hex 32 > file` writes a newline, and
   `kubectl create secret --from-file` keeps it. GitHub signs with the
   value that you paste, without the newline, so each delivery fails
   with `401 signature verification failed`. Use `--from-literal`, or
   write the file with `printf '%s'`.

4. **Each class dedupes its Tasks.** While a Task for the same issue or
   PR is active, a second label event creates nothing, and the adapter
   answers `202 {"duplicate": true, …}`. A finished Task never blocks a
   new run: remove and add the label again.
