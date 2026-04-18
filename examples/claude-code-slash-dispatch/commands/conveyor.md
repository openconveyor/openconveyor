---
description: Dispatch a task to OpenConveyor for autonomous execution
---

Dispatch the following prompt to OpenConveyor — it will run on the cluster as
an autonomous agent Job and open a PR when done.

!kubectl apply -f - <<EOF
apiVersion: openconveyor.ai/v1alpha1
kind: Task
metadata:
generateName: cc-dispatch-
namespace: conveyor-tasks
spec:
agent:
ref: claude-code-implementer
prompt:
inline: |
$ARGUMENTS

      Context: dispatched from Claude Code on $(hostname) on branch $(git branch --show-current 2>/dev/null || echo 'n/a') at $(pwd 2>/dev/null || echo '?').

permissions:
secrets: - github-token
egress: - api.github.com - github.com
resources:
cpu: "1"
memory: 1Gi
timeout: 30m
EOF

After the apply succeeds, print:

1. The created Task's name (pulled from `kubectl get tasks -n conveyor-tasks --sort-by=.metadata.creationTimestamp -o name | tail -n 1`).
2. The exact follow-along command:
   `kubectl -n conveyor-tasks logs -f job/<task-name>`.

Do not run the follow-along command yourself — the user decides whether
to tail the Job locally.
