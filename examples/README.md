# Examples

Three ways to fire the same implementer agent, proving OpenConveyor's
trigger-pluggability thesis. The reconciler, the AgentClass, and the
Task shape are identical across examples — only the trigger differs.

| Example | Trigger | What it shows |
|---|---|---|
| [`linear-claude-code/`](linear-claude-code/) | Linear webhook | SaaS issue tracker → Task |
| [`github-issues-claude-code/`](github-issues-claude-code/) | GitHub Issues webhook | Git-host issue tracker → Task |
| [`github-pr-claude-code/`](github-pr-claude-code/) | GitHub Pull Requests webhook | PR opened → review comment (reviewer archetype) |
| [`claude-code-slash-dispatch/`](claude-code-slash-dispatch/) | Claude Code slash command (laptop) | Zero-infra, zero-webhook trigger |

## Shared prerequisites

Every example assumes:

1. **OpenConveyor is installed.** See the top-level [`README.md`](../README.md);
   this directory assumes CRDs and the controller manager are running.
2. **A Task namespace exists.** The samples target `conveyor-tasks`:

   ```bash
   kubectl create namespace conveyor-tasks
   ```

3. **The implementer AgentClass is applied.** One cluster-scoped
   resource, used by all three examples:

   ```bash
   kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass.yaml
   ```

4. **Runtime secrets exist in `conveyor-tasks`.** Adjust per example:

   ```bash
   kubectl -n conveyor-tasks create secret generic anthropic-api-key \
     --from-literal=key="$ANTHROPIC_API_KEY"
   kubectl -n conveyor-tasks create secret generic github-token \
     --from-literal=token="$GITHUB_TOKEN"
   ```

5. **Webhook examples only:** the trigger adapter is running (start
   `conveyor` with `--trigger-bind-address=:9090`) and the webhook
   signing Secret lives in the adapter's namespace (default
   `conveyor-system`).
