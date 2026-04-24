# Scheduled Reporter via CronJob

This example shows how to run a reporter Task on a recurring schedule
using a stock Kubernetes `CronJob`. Per
[ADR-0003](../../docs/adr/0003-triggers-out-of-process.md), triggers
are out-of-process — the CronJob simply runs `kubectl apply` to create
a Task on each tick.

## How it works

```
CronJob (Monday 09:00)
  └─ Pod: kubectl apply -f /config/task.yaml
       └─ Task CR created in conveyor-tasks namespace
            └─ Controller reconciles → reporter Job runs
                 └─ Agent summarizes repo activity → posts GitHub Issue comment
```

## Prerequisites

1. The `claude-code-reporter` ClusterAgentClass is installed:
   ```sh
   kubectl apply -f ../../config/samples/conveyor_v1alpha1_clusteragentclass_reporter.yaml
   ```

2. The `conveyor-tasks` namespace exists:
   ```sh
   kubectl create namespace conveyor-tasks --dry-run=client -o yaml | kubectl apply -f -
   ```

3. Secrets are created in the `conveyor-tasks` namespace:
   ```sh
   kubectl -n conveyor-tasks create secret generic anthropic-api-key \
     --from-literal=token="$ANTHROPIC_API_KEY"
   kubectl -n conveyor-tasks create secret generic github-token \
     --from-literal=token="$GITHUB_TOKEN"
   ```

## Setup

1. Edit `task-configmap.yaml` — set `TARGET_REPO` and `REPORT_ISSUE_URL`
   in the prompt to match your project.

2. Apply the resources:
   ```sh
   kubectl apply -f rbac.yaml
   kubectl apply -f task-configmap.yaml
   kubectl apply -f cronjob.yaml
   ```

3. Test by triggering a manual run:
   ```sh
   kubectl -n conveyor-tasks create job --from=cronjob/weekly-report test-report
   ```

4. Watch the Task get created and the report posted:
   ```sh
   kubectl -n conveyor-tasks get tasks -w
   ```

## Customization

- **Schedule:** Edit `spec.schedule` in `cronjob.yaml`. Uses standard
  cron syntax (e.g., `0 9 * * *` for daily at 09:00 UTC).
- **Prompt:** Edit the `task.yaml` content in `task-configmap.yaml`.
- **Output:** Set `REPORT_ISSUE_URL` in the prompt to a GitHub Issue
  URL. The reporter posts the summary as a comment. Omit it to print
  to stdout only (`kubectl logs` to read).
- **Resources:** Adjust `cpu`, `memory`, and `timeout` in the Task
  template to fit your repo size.

## RBAC

The CronJob runs with a dedicated ServiceAccount (`cronjob-reporter`)
that has a single permission: `create` Tasks in `conveyor-tasks`. It
cannot read, list, or delete Tasks, and it has no access to any other
resources.
