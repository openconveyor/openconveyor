# Plugin Authoring

OpenConveyor has two plugin surfaces, both expressed as CRDs and a
container image. Neither uses runtime plugin loading — everything
an operator adds is declared in YAML and consumed by stock
Kubernetes.

> **Status.** The AgentClass and TriggerClass contracts are live.
> Two reference agent images ship in-tree (`agents/claude-code-
> implementer/`, `agents/claude-code-reviewer/`) and four worked
> examples wire them up under `examples/`.

## AgentClass

An agent is a container image that reads a prompt, does its work,
and exits. Nothing more. The `ClusterAgentClass` CRD binds a name
to an image and declares how the runtime hands the prompt in.

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: ClusterAgentClass
metadata:
  name: claude-code-implementer
spec:
  image: ghcr.io/openconveyor/agent-claude:latest
  inputs:
    prompt: { mount: /task/prompt }
  requires:
    egress: [api.anthropic.com]
```

### Contract

1. **Entrypoint reads the prompt** from the path in `spec.inputs.prompt.mount`
   (or the env var in `spec.inputs.prompt.env`).
2. **Exit codes are meaningful.** Zero = success, non-zero = failure.
   OpenConveyor surfaces Job conditions to `Task.status.phase`.
3. **No daemons.** This is a one-shot Job, not a server. The image's
   process exits.
4. **No writes outside `/tmp`.** `readOnlyRootFilesystem: true` is
   enforced. Use `/tmp` (provided as an emptyDir) or declare a secret
   volume as your writable scratch.
5. **Do not expect a ServiceAccount token** unless the Task declared
   `permissions.rbac`. Agent images should not assume they can call
   the k8s API.

### Archetypes we plan to ship

| Archetype | Example prompt | Typical permissions |
|---|---|---|
| **Implementer** | "Fix this bug and open a PR" | `github-token` secret; egress to Anthropic + GitHub |
| **Reviewer** | "Review this PR diff and comment" | `github-token` with PR-comment scope only; no `git push` |
| **Reporter** | "Summarize last week's PR metrics" | Read-only GitHub token; egress to Telegram for output |

## TriggerClass

A trigger is anything that creates a `Task` CR. That is the whole
contract — there is no SDK.

The reference trigger is the webhook adapter (Phase 4), configured
via `ClusterTriggerClass`:

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: ClusterTriggerClass
metadata:
  name: github-issues
spec:
  path: /webhooks/github-issues
  signature:
    type: hmac-sha256
    secretRef: { name: github-webhook-secret, key: secret }
  filters:
    - path: action
      equals: labeled
    - path: label.name
      equals: conveyor:ready
  task:
    namespace: conveyor-tasks
    generateNamePrefix: gh-issue-
    agent: { ref: claude-code-implementer }
    permissions:
      secrets: [anthropic-api-key, github-token]
      egress:  [api.anthropic.com, api.github.com, github.com]
    resources:
      timeout: 30m
    mappings:
      prompt.inline: "Fix: {{.issue.title}}\n\n{{.issue.body}}"
```

### Building a trigger that is not a webhook

Anything that `kubectl apply`s a Task is a trigger. Examples already
planned:

- **Scheduled (Phase 9):** a stock K8s `CronJob` that `kubectl apply`s
  a Task YAML.
- **Slash command (Phase 5):** a Claude Code `/conveyor <prompt>`
  slash command that expands to a `kubectl apply`.
- **Custom scripts:** anything in bash or Python that can hit the
  k8s API.

None of these require registering anything with OpenConveyor. They just
need RBAC to create Task CRs in a namespace.

## Publishing an AgentClass

For now, drop the YAML into your cluster directly:

```sh
kubectl apply -f my-agent.yaml
```

Once the plugin ecosystem matures we will add a registry convention
(likely an OCI artifact pointing at the `ClusterAgentClass` YAML + the
image digest) so third-party agents can be installed via a single
reference. Not in v1.
