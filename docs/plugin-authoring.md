# Plugin authoring

OpenConveyor has two extension points. An **agent** is a container
image that a `ClusterAgentClass` describes. A **trigger** is anything
that creates a `Task`. Both extension points are plain Kubernetes
objects. The controller loads no plugin code, and a new agent or a new
trigger never changes `internal/`.

This guide describes the two contracts as the code implements them.
That code is the types in `api/v1alpha1/`, the Job builder in
`internal/controller/build.go`, the projections in `internal/policy/`
and the webhook adapter in `internal/trigger/`. The three reference
agents under `agents/` show the agent contract in use. The five
examples under `examples/` show the trigger contract in use.

## Agents

### The agent contract

The controller starts the agent image as the single container `agent`
in a one-shot Job. The container finds its inputs at fixed locations:

| Input | Location | Rule |
|---|---|---|
| Prompt | The file at `spec.inputs.prompt.mount` of the class, for example `/task/prompt` | The controller mounts one read-only file. The agent can `cat` it. |
| Secrets | One directory for each Secret at `/run/secrets/<name>/<key>` | One read-only file for each Secret key. Secrets never become environment variables (ADR-0004). |
| Config | Environment variables, plus one JSON file or one JSON variable when the class declares it | See [Config projection](#config-projection). |
| Scratch space | `/tmp`, an `emptyDir` volume | The root filesystem rejects writes. Write all state under `/tmp`. |
| Network | The egress allowlist of the Task and of the class | Without entries, the NetworkPolicy denies all egress. The allowlist opens port 443 by default, plus DNS on port 53. |
| Kubernetes API | No ServiceAccount token unless `Task.spec.permissions.rbac` has rules | Do not assume that the agent can call the Kubernetes API. |

The Job runs the container as UID and GID 65532, with `runAsNonRoot`,
the `RuntimeDefault` seccomp profile, no privilege escalation and no
capabilities. [`docs/security-model.md`](security-model.md) lists the
guarantees behind these settings.

An agent obeys these rules:

1. Read the prompt, do the work, and exit. Exit code 0 means success.
   A non-zero exit code means failure. The controller maps the Job
   result to `Task.status.phase`.
2. Do not start a server. The Job runs once, and `backoffLimit` is 0,
   so Kubernetes never starts a second pod after a failure.
3. Finish before the timeout. `Task.spec.resources.timeout` becomes
   `Job.spec.activeDeadlineSeconds`. After the deadline, Kubernetes
   stops the pod and the Task becomes `TimedOut`.
4. Write to stdout and stderr. `kubectl logs job/<task-name> -c agent`
   is the only log channel.
5. Keep all writable state under `/tmp`. Set `HOME=/tmp` in the image
   when the tools need a home directory.

### ClusterAgentClass

The class binds a name to an image and tells the controller where the
image expects its inputs. This is the reference implementer class from
[`config/samples/`](../config/samples/):

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: ClusterAgentClass
metadata:
  name: claude-code-implementer
spec:
  image: ghcr.io/openconveyor/agent-claude-implementer:v0.2.1
  imagePullPolicy: IfNotPresent
  inputs:
    prompt:
      mount: /task/prompt
  requires:
    egress:
      - api.anthropic.com
    secrets:
      - anthropic-api-key
```

| Field | Required | Meaning |
|---|---|---|
| `spec.image` | Yes | The OCI reference of the agent image. |
| `spec.imagePullPolicy` | No | `Always`, `Never` or `IfNotPresent`. The default is `IfNotPresent`. |
| `spec.command`, `spec.args` | No | Replace the `ENTRYPOINT` and the `CMD` of the image. |
| `spec.inputs.prompt.mount` | No | The absolute file path at which the controller mounts the prompt. Without it, the controller mounts no prompt. |
| `spec.inputs.prompt.env` | No | The schema accepts it, but the controller does not deliver the prompt through a variable at this time. Set `mount`. |
| `spec.inputs.config.mount` | No | The absolute file path at which the controller mounts the full `Task.spec.agent.config` JSON. |
| `spec.inputs.config.env` | No | The variable that carries the full `Task.spec.agent.config` JSON. |
| `spec.requires.egress` | No | DNS names, IP addresses or CIDR blocks that every Task of this class needs, for example the model API. |
| `spec.requires.secrets` | No | Secret names that every Task of this class needs, for example the model API key. |

The controller reads the class at reconcile time and does not
reconcile the class itself. It unions `spec.requires.egress` with
`Task.spec.permissions.egress`, and `spec.requires.secrets` with
`Task.spec.permissions.secrets`. A Task therefore lists only the
permissions that are specific to it. A Task whose class does
not exist fails with the `Ready` reason `AgentClassMissing`.

An agent without a prompt mount is valid. A reporter with the prompt
built into the image is an example. The controller then mounts no
prompt, and the Task still needs a `spec.prompt` because the Task
schema requires one.

### Config projection

`Task.spec.agent.config` is a JSON object for non-secret settings. The
controller delivers it in up to three ways:

1. Each top-level scalar field (string, number or boolean) becomes an
   environment variable with the name of the field. The controller
   sorts the names. A name must match `^[A-Za-z_][A-Za-z0-9_]*$`.
2. When the class sets `spec.inputs.config.mount`, the controller
   writes the full JSON to an owned ConfigMap and mounts it as one
   read-only file at that path.
3. When the class sets `spec.inputs.config.env`, the controller puts
   the full JSON into that variable.

A nested object, an array, a `null` value or a field with an invalid
name cannot become a variable. The agent reads such a field from the
full JSON only. When the class declares no `config` input, such a
field is a permanent error, and the Task fails with the `Ready` reason
`InvalidSpec`.

> [!WARNING]
> Do not put a secret in `spec.agent.config`.
> Environment variables leak into `/proc` and into child processes.
> Declare a Secret in `spec.permissions.secrets` instead (ADR-0004).

This Task sets two variables for the reporter. The class declares no
`config` input, so only scalar fields are valid here:

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: Task
metadata:
  name: weekly-report
  namespace: conveyor-tasks
spec:
  agent:
    ref: claude-code-reporter
    config:
      TARGET_REPO: your-org/your-repo
      REPORT_ISSUE_URL: https://github.com/your-org/your-repo/issues/1
  prompt:
    inline: |
      Summarize the PRs merged and the issues opened in the past week.
  permissions:
    secrets:
      - github-token
    egress:
      - api.github.com
  resources:
    cpu: "500m"
    memory: 512Mi
    timeout: 10m
```

### Build the image

The reference Dockerfiles under [`agents/`](../agents/) show the
pattern. An image for OpenConveyor does these things:

- Pin the base image to a tag and a digest.
- Install the tools that the entrypoint calls.
- Create a user with UID and GID 65532 and the home directory `/tmp`.
  Tools such as `git` and `gh` then find a writable home on the
  read-only root filesystem.
- Copy the entrypoint, set `USER 65532:65532` and `ENV HOME=/tmp`.
- Set the `ENTRYPOINT` to the script that reads the prompt and exits.

The entrypoint of a reference agent exports each Secret as a variable
before it calls the tools. It converts the Secret name to upper case
and replaces `-` with `_`: `anthropic-api-key` becomes
`$ANTHROPIC_API_KEY`, and `github-token` becomes `$GITHUB_TOKEN`. The
value is the content of the single file in the Secret directory. Give
each Secret one key, because the loop exports the same variable once
for each file. This export is a choice of the reference agents, not a
rule of the controller. Your agent can read the files directly.

### Reference agents

A **reference agent** is one of the three agent images that this repo
ships. Each one is a worked example of the agent contract, not the
only agent that can run. Each image contains the Claude Code CLI, `git`
and `gh`. The release workflow pushes them to `ghcr.io` with the same
tag as the controller image.

| Archetype | Image | What it does | Side effect on the git host |
|---|---|---|---|
| Implementer | `ghcr.io/openconveyor/agent-claude-implementer` | Clones a repo, sends the prompt to `claude`, commits the diff, and opens a PR. Without a repo, it answers the prompt and exits. | `git push` and `gh pr create` |
| Reviewer | `ghcr.io/openconveyor/agent-claude-reviewer` | Reads the PR diff with `gh pr diff`, sends the prompt and the diff to `claude`, and posts the review. | `gh pr review` only |
| Reporter | `ghcr.io/openconveyor/agent-claude-reporter` | Lists PRs and issues with `gh`, sends the prompt and the data to `claude`, and posts the report as a GitHub Issue comment. | `gh issue comment` only |

The classes for the three archetypes are in
[`config/samples/`](../config/samples/). All three declare
`inputs.prompt.mount: /task/prompt` and require `api.anthropic.com`
and the `anthropic-api-key` Secret. A Task adds the git host and the
`github-token` Secret. Scope the token to the side effect in the
table: the reviewer and the reporter never push code.

The sample Task in `config/samples/conveyor_v1alpha1_task.yaml` sets
no `TARGET_REPO`. The implementer then answers the prompt and exits
without a clone, a commit or a PR. The quick start uses this as a
smoke test, which completes with only the two Secrets in place.

### Environment variables of the reference agents

The entrypoints under `agents/*/entrypoint.sh` read these variables.
"Secret" means that the entrypoint exports the variable from the
Secret file. "Config" means that the controller projects the variable
from `Task.spec.agent.config`. "Image" means that the Dockerfile sets
a default with `ENV`.

| Variable | Origin | Agents | Meaning |
|---|---|---|---|
| `ANTHROPIC_API_KEY` | Secret `anthropic-api-key` | All three | The `claude` CLI reads it. The entrypoint exports it and does not read it. |
| `GITHUB_TOKEN` | Secret `github-token` | All three | `gh` reads it. The implementer also stores it in the `git` credential helper for `git push`. The reviewer, and the reporter with a `TARGET_REPO`, exit with an error when it is empty. |
| `GITHUB_TOKEN_FILE` | Config | Implementer | A file path. The entrypoint reads the token from this file only when `GITHUB_TOKEN` is empty. |
| `GIT_HOST` | Image (`github`), Config | Implementer, Reviewer | The git host. Only `github` is valid. Any other value is an error. |
| `TARGET_REPO` | Config | Implementer, Reporter | Implementer: the git URL to clone, for example `https://github.com/your-org/your-repo.git`. Reporter: the `owner/name` form for `gh`. Without it, the implementer answers the prompt only, and the reporter sends the prompt without repo data. |
| `TARGET_BRANCH` | Config | Implementer | The branch to clone and the base of the PR. The default is `main`. |
| `BRANCH_NAME` | Config | Implementer | The name of the branch that the agent pushes. The default is `conveyor/<timestamp>`. |
| `PR_URL` | Config | Reviewer | The PR to review. Without it, the entrypoint takes the first `https://<host>/<owner>/<repo>/pull/<n>` URL in the prompt. |
| `REVIEW_EVENT` | Config | Reviewer | `COMMENT` (the default), `APPROVE` or `REQUEST_CHANGES`. |
| `REPORT_ISSUE_URL` | Config | Reporter | The GitHub Issue that receives the report as a comment. Without it, the report goes to stdout only. |
| `PROMPT_FILE`, `SECRETS_ROOT`, `WORKDIR` | Config | All three | Replace the defaults `/task/prompt`, `/run/secrets` and `/tmp/work`. Set them only for a local test of the script. |

The reference trigger for GitHub Issues maps `repository.clone_url` to
`config.TARGET_REPO` and `repository.default_branch` to
`config.TARGET_BRANCH`. The reviewer trigger maps
`pull_request.html_url` to the prompt, and the reviewer extracts the
PR URL from there.

### Register the class

A class is a cluster-scoped object. Apply it with `kubectl`:

```sh
kubectl apply -f config/samples/conveyor_v1alpha1_clusteragentclass.yaml
```

Then create a Task that names the class in `spec.agent.ref`, and
watch the phase:

```sh
kubectl -n conveyor-tasks get task -w
kubectl -n conveyor-tasks logs job/<task-name> -c agent
```

There is no registry for agent classes. A third-party agent ships a
`ClusterAgentClass` YAML next to its image.

## Triggers

### Any Task creator is a trigger

The whole trigger contract is: create a `Task` object. There is no SDK
and no registration. A trigger needs only the RBAC permission to
create `tasks.openconveyor.ai` in the target namespace. Two examples
under `examples/` use no webhook at all:

- [`cronjob-reporter/`](../examples/cronjob-reporter/): a stock
  Kubernetes `CronJob` runs `kubectl apply` on a Task template on each
  tick.
- [`claude-code-slash-dispatch/`](../examples/claude-code-slash-dispatch/):
  a Claude Code slash command on a laptop expands to `kubectl apply`.

Any shell script, CI job or service with a kubeconfig works the same
way. See [ADR-0003](adr/0003-triggers-out-of-process.md).

### The webhook adapter

The controller binary contains one trigger: the webhook adapter. The
flag `--trigger-bind-address` enables it. The adapter reads a
`ClusterTriggerClass` for each HTTP path and creates a Task from the
template in that class. The signing Secrets are in the namespace of
the adapter: the flag `--trigger-namespace`, or `POD_NAMESPACE`, or
`conveyor-system`.

For one `POST /<spec.path>` request, the adapter does these steps in
this order:

1. It reads the body, up to 1 MiB, and rejects a body that is not
   JSON.
2. It finds the `ClusterTriggerClass` whose `spec.path` is equal to
   the request path.
3. It reads the signing Secret and verifies the HMAC of the body.
4. It applies the filters. A filter that does not match ends the
   request with no Task.
5. It builds the Task from the template and the mappings, and creates
   it with the Kubernetes API.

| Status | Meaning |
|---|---|
| `200` | The adapter created a Task. The body names it. |
| `202` | The signature is valid, but a filter did not match. No Task. |
| `400` | The body is not JSON, or the mappings produced a Task without a prompt. |
| `401` | The signature header is missing or does not match. |
| `404` | No `ClusterTriggerClass` has this path. |
| `405` | The method is not `POST`. |
| `413` | The body is larger than 1 MiB. |
| `500` | The Secret is missing or empty, the algorithm is unknown, or the Task create failed. |

### ClusterTriggerClass

This is the reference class from
[`config/samples/conveyor_v1alpha1_clustertriggerclass.yaml`](../config/samples/conveyor_v1alpha1_clustertriggerclass.yaml).
It creates an implementer Task when a user labels a GitHub issue with
`conveyor:ready`:

```yaml
apiVersion: openconveyor.ai/v1alpha1
kind: ClusterTriggerClass
metadata:
  name: github-issues
spec:
  path: /github
  signature:
    header: X-Hub-Signature-256
    algorithm: sha256
    prefix: "sha256="
    secretRef:
      name: github-webhook
      key: secret
  filters:
    - path: action
      equals: labeled
    - path: label.name
      equals: "conveyor:ready"
  task:
    namespace: conveyor-tasks
    generateNamePrefix: gh-issue-
    agent:
      ref: claude-code-implementer
    permissions:
      secrets:
        - github-token
      egress:
        - api.github.com
        - github.com
    resources:
      cpu: "1"
      memory: 1Gi
      timeout: 30m
    mappings:
      - from: issue.title
        to: prompt
      - from: issue.number
        to: labels.openconveyor.ai/issue-number
      - from: sender.login
        to: annotations.openconveyor.ai/requested-by
        default: unknown
```

#### Signature

The adapter supports the HMAC scheme that GitHub, Linear, GitLab and
Forgejo use. [ADR-0006](adr/0006-webhook-adapter-trust-boundary.md)
records the trust boundary.

| Field | Required | Meaning |
|---|---|---|
| `header` | Yes | The HTTP header with the signature, for example `X-Hub-Signature-256` or `Linear-Signature`. |
| `algorithm` | No | Only `sha256` is valid. It is the default. |
| `prefix` | No | The text that the adapter removes from the header value before the comparison. GitHub sends `sha256=<hex>`, so the prefix is `sha256=`. Linear sends the raw hex, so the prefix is empty. |
| `secretRef` | Yes | The Secret name and key with the shared signing secret, in the namespace of the adapter. |

The adapter compares the hex digest in constant time. An empty Secret
is a configuration error and gives a `500`, not a `401`. Use at least
32 bytes of entropy for the secret, and rotate it by an edit of the
Secret only. The adapter reads the Secret on each request.

#### Filters

Each filter has a gjson `path` into the body and an `equals` string.
The adapter compares the string form of the value at that path with
`equals`, so a number such as `42` matches `"42"`. All filters must
match.

> [!WARNING]
> Always set at least one filter.
> Without filters, every event with a valid signature creates a Task,
> for example each comment and each close of an issue.

#### Mappings

A mapping copies one value from the body to one field of the Task. It
has a gjson `from` path, a `to` target and an optional `default`. The
adapter uses `default` when the path does not exist or has an empty
value. A mapping with no value and no `default` has no effect. A Task
without a prompt is an error (`400`), so give the `prompt` mapping a
`default` when the field can be absent.

| `to` | Target on the Task |
|---|---|
| `prompt` | `spec.prompt.inline` |
| `name` | `metadata.name`. It replaces the generated name. |
| `labels.<key>` | `metadata.labels[<key>]` |
| `annotations.<key>` | `metadata.annotations[<key>]` |
| `config.<key>` | `spec.agent.config.<key>`, which the controller projects as the variable `$<key>` |

There is no templating. A mapping copies one payload field without
change, and you cannot combine two fields or add fixed text. To give
the agent instructions together with payload data, put the
instructions in the issue body or the PR body and map that field to
`prompt`. A `config.<key>` mapping replaces a value with the same key
in the template `agent.config`.

#### Task template

`spec.task` is the prototype of every Task that the class creates.

| Field | Required | Meaning |
|---|---|---|
| `namespace` | No | The namespace of the Tasks. The default is the namespace of the adapter. |
| `generateNamePrefix` | No | The prefix of the generated Task name. The default is `task-`. A `name` mapping replaces the whole name. |
| `agent` | Yes | The `ref` and the optional `config`, copied into each Task. |
| `permissions` | No | `secrets`, `egress`, `egressPorts` and `rbac`, copied into each Task. |
| `resources` | Yes | `cpu`, `memory` and the mandatory `timeout`, copied into each Task. |
| `ttlSecondsAfterFinished` | No | The Task TTL. Set it, because webhook Tasks accumulate without it. The examples use `86400`. |
| `mappings` | No | The list of mappings above. |

The adapter needs RBAC to create Tasks in the target namespace. The
Helm chart and `config/rbac/` grant it.

### Examples that ship

The five examples under [`examples/`](../examples/) cover both trigger
kinds and all three reference agents:

| Example | Trigger | Agent |
|---|---|---|
| [`github-issues-claude-code/`](../examples/github-issues-claude-code/) | GitHub Issues webhook | Implementer |
| [`linear-claude-code/`](../examples/linear-claude-code/) | Linear webhook | Implementer |
| [`github-pr-claude-code/`](../examples/github-pr-claude-code/) | GitHub Pull Requests webhook | Reviewer |
| [`claude-code-slash-dispatch/`](../examples/claude-code-slash-dispatch/) | Claude Code slash command | Implementer |
| [`cronjob-reporter/`](../examples/cronjob-reporter/) | Kubernetes CronJob | Reporter |

Copy the one that is nearest to your case, and change the class, the
filters and the mappings.
