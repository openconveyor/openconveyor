# Examples

Five ways to start agents. They prove the trigger-pluggability thesis
of OpenConveyor. The reconciler, the AgentClass, and the Task shape are
identical across the examples. Only the trigger differs.

| Example | Trigger | What it shows |
|---|---|---|
| [`linear-claude-code/`](linear-claude-code/) | Linear webhook | SaaS issue tracker → Task |
| [`github-issues-claude-code/`](github-issues-claude-code/) | GitHub Issues webhook | Git-host issue tracker → Task |
| [`github-pr-claude-code/`](github-pr-claude-code/) | GitHub Pull Requests webhook | PR opened → review comment (reviewer archetype) |
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
   cluster-scoped, one per archetype. The Linear, GitHub Issues and
   slash-command examples use the implementer. The PR example uses the
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
