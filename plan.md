# Self-Hosted Agent Pipeline: Ticket → Code → PR → Deploy

## Overview

A self-hosted, agent-driven development pipeline that turns Linear tickets into deployed code on a homelab k3s cluster. The system uses Claude Code headless as the coding agent, Linear as the Kanban board, Tekton for CI, ArgoCD for CD, and Telegram for notifications and approval gating.

```
┌─────────────────┐     ┌──────────────┐     ┌───────────────────┐
│  Claude Chat     │────▶│  Linear      │────▶│  Orchestrator     │
│  (mobile/tablet) │     │  (Kanban)    │     │  (k3s pod)        │
│  Create & refine │     │  Refine,     │     │  Webhook listener │
│  tickets via MCP │     │  drag to     │     │                   │
│                  │     │  "Ready"     │     │                   │
└─────────────────┘     └──────────────┘     └────────┬──────────┘
                                                       │
                                              spawns K8s Job
                                                       │
                                                       ▼
                                             ┌───────────────────┐
                                             │  Claude Code      │
                                             │  Headless Agent   │
                                             │  (container)      │
                                             │                   │
                                             │  - Read/Edit code │
                                             │  - MCP: Context7  │
                                             │  - MCP: Exa       │
                                             │  - MCP: Chromium  │
                                             └────────┬──────────┘
                                                       │
                                                  git push + gh pr create
                                                       │
                                                       ▼
                                             ┌───────────────────┐
                                             │  GitHub           │
                                             │  Pull Request     │
                                             └────────┬──────────┘
                                                       │
                                              PR webhook fires
                                                       │
                                          ┌────────────┴────────────┐
                                          ▼                         ▼
                                ┌──────────────────┐     ┌──────────────────┐
                                │  Tekton CI       │     │  Telegram Bot    │
                                │  Build, test,    │     │  "PR #17 opened  │
                                │  image push      │     │   for LIN-42"   │
                                └────────┬─────────┘     └──────────────────┘
                                         │
                                    on merge
                                         │
                                         ▼
                                ┌──────────────────┐     ┌──────────────────┐
                                │  ArgoCD          │────▶│  Telegram Bot    │
                                │  Sync to k3s     │     │  "LIN-42         │
                                │                  │     │   deployed"      │
                                └──────────────────┘     └──────────────────┘
```

---

## Phase 1: Linear Setup

### Goal

Establish Linear as the Kanban board with a status-driven workflow and webhook integration.

### Tasks

#### 1.1 Create Linear workspace and project

- Create a workspace (free tier: 250 issues)
- Create a project per domain (e.g., `homelab-infra`, `homelab-apps`, `blog`)
- Define statuses: `Backlog` → `Refinement` → `Ready` → `Agent Working` → `In Review` → `Done`

#### 1.2 Create issue template

Every ticket the agent picks up needs structured input. Define a Linear issue template or convention:

```markdown
## User Story

As a [role], I want [feature] so that [benefit].

## Acceptance Criteria

- [ ] Criterion 1
- [ ] Criterion 2

## Target Repository

github.com/<org>/<repo>

## Affected Files/Modules

- src/path/to/module (if known)

## Additional Context

Links, diagrams, related issues.
```

#### 1.3 Configure Linear webhook

- Go to Settings → API → Webhooks
- URL: `https://<your-webhook-endpoint>/linear`
- Events: `Issue` → `update` (fires on status change)
- The webhook payload includes `data.stateId` — filter for the `Ready` state ID

#### 1.4 Generate Linear API key

- Settings → API → Personal API Keys
- Store in OpenBao (your homelab secrets backend)
- Needed for: reading ticket details, updating status, posting comments

### Deliverables

- [ ] Linear workspace with project(s)
- [ ] Issue template documented
- [ ] Webhook configured and pointing at orchestrator endpoint
- [ ] API key stored in OpenBao

---

## Phase 2: Orchestrator Service

### Goal

A lightweight service on k3s that receives Linear webhooks and spawns Claude Code agent Jobs.

### Architecture Decision

Use a small Go or Python HTTP service (not Tekton EventListener — Linear's payload format is custom and mapping it through TriggerBindings adds unnecessary friction).

### Tasks

#### 2.1 Webhook receiver

Receives Linear webhook, validates signature, extracts ticket data, spawns a Kubernetes Job.

```python
# orchestrator/main.py — minimal FastAPI webhook receiver
from fastapi import FastAPI, Request, HTTPException
from kubernetes import client, config
import httpx
import json
import os
import hashlib
import hmac

app = FastAPI()

LINEAR_WEBHOOK_SECRET = os.environ["LINEAR_WEBHOOK_SECRET"]
LINEAR_API_KEY = os.environ["LINEAR_API_KEY"]
READY_STATE_ID = os.environ["LINEAR_READY_STATE_ID"]
NAMESPACE = os.environ.get("NAMESPACE", "agent-pipeline")

config.load_incluster_config()
batch_v1 = client.BatchV1Api()


def verify_signature(body: bytes, signature: str) -> bool:
    expected = hmac.new(
        LINEAR_WEBHOOK_SECRET.encode(), body, hashlib.sha256
    ).hexdigest()
    return hmac.compare_digest(expected, signature)


@app.post("/linear")
async def handle_linear_webhook(request: Request):
    body = await request.body()
    signature = request.headers.get("Linear-Signature", "")

    if not verify_signature(body, signature):
        raise HTTPException(status_code=401, detail="Invalid signature")

    payload = json.loads(body)
    action = payload.get("action")
    data = payload.get("data", {})

    # Only trigger on status change to "Ready"
    if action != "update":
        return {"status": "ignored"}

    new_state_id = data.get("stateId")
    if new_state_id != READY_STATE_ID:
        return {"status": "ignored", "reason": "not ready state"}

    issue_id = data.get("id")
    issue = await fetch_issue_details(issue_id)

    # Extract repo from description (convention: "## Target Repository" section)
    repo = extract_repo(issue["description"])
    if not repo:
        await update_issue_comment(
            issue_id, "Agent: no target repository found in ticket."
        )
        return {"status": "error", "reason": "no repo"}

    # Update Linear status to "Agent Working"
    await update_issue_state(issue_id, os.environ["AGENT_WORKING_STATE_ID"])

    # Send Telegram notification
    await notify_telegram(f"Agent picked up {issue['identifier']}: {issue['title']}")

    # Spawn K8s Job
    spawn_agent_job(
        issue_id=issue_id,
        issue_identifier=issue["identifier"],
        title=issue["title"],
        description=issue["description"],
        repo=repo,
    )

    return {"status": "spawned"}


async def fetch_issue_details(issue_id: str) -> dict:
    query = """
    query($id: String!) {
        issue(id: $id) {
            id
            identifier
            title
            description
            url
        }
    }
    """
    async with httpx.AsyncClient() as client:
        resp = await client.post(
            "https://api.linear.app/graphql",
            headers={
                "Authorization": LINEAR_API_KEY,
                "Content-Type": "application/json",
            },
            json={"query": query, "variables": {"id": issue_id}},
        )
        return resp.json()["data"]["issue"]


def extract_repo(description: str) -> str | None:
    """Extract repo from '## Target Repository' section."""
    if not description:
        return None
    for line in description.split("\n"):
        line = line.strip()
        if line.startswith("github.com/") or line.startswith("https://github.com/"):
            return line.removeprefix("https://").removeprefix("github.com/")
    return None


def spawn_agent_job(
    issue_id: str,
    issue_identifier: str,
    title: str,
    description: str,
    repo: str,
):
    job_name = f"agent-{issue_identifier.lower().replace('-', '')}"

    job = client.V1Job(
        metadata=client.V1ObjectMeta(
            name=job_name,
            namespace=NAMESPACE,
            labels={
                "app": "claude-agent",
                "linear-issue": issue_identifier,
            },
        ),
        spec=client.V1JobSpec(
            ttl_seconds_after_finished=3600,
            backoff_limit=0,
            template=client.V1PodTemplateSpec(
                spec=client.V1PodSpec(
                    restart_policy="Never",
                    containers=[
                        client.V1Container(
                            name="agent",
                            image=os.environ["AGENT_IMAGE"],
                            env=[
                                client.V1EnvVar(
                                    name="ISSUE_ID", value=issue_id
                                ),
                                client.V1EnvVar(
                                    name="ISSUE_IDENTIFIER",
                                    value=issue_identifier,
                                ),
                                client.V1EnvVar(name="ISSUE_TITLE", value=title),
                                client.V1EnvVar(
                                    name="ISSUE_DESCRIPTION", value=description
                                ),
                                client.V1EnvVar(name="TARGET_REPO", value=repo),
                                client.V1EnvVar(
                                    name="ANTHROPIC_API_KEY",
                                    value_from=client.V1EnvVarSource(
                                        secret_key_ref=client.V1SecretKeySelector(
                                            name="agent-secrets",
                                            key="anthropic-api-key",
                                        )
                                    ),
                                ),
                                client.V1EnvVar(
                                    name="GH_TOKEN",
                                    value_from=client.V1EnvVarSource(
                                        secret_key_ref=client.V1SecretKeySelector(
                                            name="agent-secrets",
                                            key="github-token",
                                        )
                                    ),
                                ),
                                client.V1EnvVar(
                                    name="LINEAR_API_KEY",
                                    value_from=client.V1EnvVarSource(
                                        secret_key_ref=client.V1SecretKeySelector(
                                            name="agent-secrets",
                                            key="linear-api-key",
                                        )
                                    ),
                                ),
                            ],
                            resources=client.V1ResourceRequirements(
                                requests={"memory": "512Mi", "cpu": "250m"},
                                limits={"memory": "1Gi", "cpu": "1"},
                            ),
                        )
                    ],
                )
            ),
        ),
    )

    batch_v1.create_namespaced_job(namespace=NAMESPACE, body=job)
```

#### 2.2 Kubernetes manifests for orchestrator

```yaml
# orchestrator/k8s/deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agent-orchestrator
  namespace: agent-pipeline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: agent-orchestrator
  template:
    metadata:
      labels:
        app: agent-orchestrator
    spec:
      serviceAccountName: agent-orchestrator
      containers:
        - name: orchestrator
          image: ghcr.io/<org>/agent-orchestrator:latest
          ports:
            - containerPort: 8000
          env:
            - name: LINEAR_WEBHOOK_SECRET
              valueFrom:
                secretKeyRef:
                  name: agent-secrets
                  key: linear-webhook-secret
            - name: LINEAR_API_KEY
              valueFrom:
                secretKeyRef:
                  name: agent-secrets
                  key: linear-api-key
            - name: LINEAR_READY_STATE_ID
              value: "<your-ready-state-uuid>"
            - name: AGENT_WORKING_STATE_ID
              value: "<your-agent-working-state-uuid>"
            - name: AGENT_IMAGE
              value: "ghcr.io/<org>/claude-agent:latest"
            - name: NAMESPACE
              value: "agent-pipeline"
            - name: TELEGRAM_BOT_TOKEN
              valueFrom:
                secretKeyRef:
                  name: agent-secrets
                  key: telegram-bot-token
            - name: TELEGRAM_CHAT_ID
              valueFrom:
                secretKeyRef:
                  name: agent-secrets
                  key: telegram-chat-id
---
apiVersion: v1
kind: Service
metadata:
  name: agent-orchestrator
  namespace: agent-pipeline
spec:
  selector:
    app: agent-orchestrator
  ports:
    - port: 8000
      targetPort: 8000
---
# RBAC: orchestrator needs permission to create Jobs
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: agent-orchestrator
  namespace: agent-pipeline
rules:
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["create", "get", "list", "watch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: agent-orchestrator
  namespace: agent-pipeline
subjects:
  - kind: ServiceAccount
    name: agent-orchestrator
    namespace: agent-pipeline
roleRef:
  kind: Role
  name: agent-orchestrator
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: agent-orchestrator
  namespace: agent-pipeline
```

#### 2.3 Ingress / webhook exposure

Expose the orchestrator endpoint to receive Linear webhooks. Options:

- **Cloudflare Tunnel** (preferred for homelab, no port forwarding)
- **Tailscale Funnel**
- **Nginx Ingress + cert-manager + DynDNS**

```yaml
# Example: Cloudflare Tunnel via cloudflared
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cloudflared
  namespace: agent-pipeline
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cloudflared
  template:
    spec:
      containers:
        - name: cloudflared
          image: cloudflare/cloudflared:latest
          args:
            - tunnel
            - --no-autoupdate
            - run
            - --token
            - $(TUNNEL_TOKEN)
          env:
            - name: TUNNEL_TOKEN
              valueFrom:
                secretKeyRef:
                  name: agent-secrets
                  key: cloudflare-tunnel-token
```

### Deliverables

- [ ] Orchestrator service (Python/Go)
- [ ] Dockerfile for orchestrator
- [ ] K8s manifests (Deployment, Service, RBAC, ServiceAccount)
- [ ] Webhook endpoint exposed via tunnel
- [ ] Linear webhook verified end-to-end

---

## Phase 3: Claude Code Agent Container

### Goal

A container image that receives a ticket, clones a repo, runs Claude Code headless with MCP tools, and opens a PR.

### Tasks

#### 3.1 Agent entrypoint script

```bash
#!/bin/bash
# agent/ticket-agent.sh
set -euo pipefail

# Inputs from environment (set by orchestrator Job)
: "${ISSUE_ID:?}"
: "${ISSUE_IDENTIFIER:?}"
: "${ISSUE_TITLE:?}"
: "${ISSUE_DESCRIPTION:?}"
: "${TARGET_REPO:?}"
: "${ANTHROPIC_API_KEY:?}"
: "${GH_TOKEN:?}"
: "${LINEAR_API_KEY:?}"

WORKDIR=$(mktemp -d)
BRANCH="agent/${ISSUE_IDENTIFIER}"
OUTPUT_FILE="/tmp/claude-output.json"

# --- Helper functions ---

linear_comment() {
  local issue_id="$1"
  local body="$2"
  curl -s -X POST https://api.linear.app/graphql \
    -H "Authorization: $LINEAR_API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"query\": \"mutation { commentCreate(input: { issueId: \\\"$issue_id\\\", body: \\\"$body\\\" }) { success } }\"}" \
    > /dev/null
}

linear_set_state() {
  local issue_id="$1"
  local state_id="$2"
  curl -s -X POST https://api.linear.app/graphql \
    -H "Authorization: $LINEAR_API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"query\": \"mutation { issueUpdate(id: \\\"$issue_id\\\", input: { stateId: \\\"$state_id\\\" }) { success } }\"}" \
    > /dev/null
}

notify_telegram() {
  if [ -n "${TELEGRAM_BOT_TOKEN:-}" ] && [ -n "${TELEGRAM_CHAT_ID:-}" ]; then
    curl -s "https://api.telegram.org/bot${TELEGRAM_BOT_TOKEN}/sendMessage" \
      -d "chat_id=${TELEGRAM_CHAT_ID}" \
      -d "text=$1" \
      -d "parse_mode=Markdown" > /dev/null
  fi
}

# --- Main ---

echo "=== Agent starting for $ISSUE_IDENTIFIER ==="

# Clone repo
gh repo clone "$TARGET_REPO" "$WORKDIR"
cd "$WORKDIR"

# Create branch
git checkout -b "$BRANCH"

# Build prompt
PROMPT=$(cat <<EOF
You are implementing a ticket for a software project.

## Ticket: ${ISSUE_IDENTIFIER}
## Title: ${ISSUE_TITLE}

## Description
${ISSUE_DESCRIPTION}

## Instructions
1. Read the existing codebase to understand conventions, patterns, and structure.
2. Implement the changes described in the ticket.
3. Follow existing code style, naming conventions, and patterns.
4. Write or update tests if the project has a test suite.
5. Only modify files relevant to the ticket.
6. Stage all your changes with git add when done.

Do NOT run builds, installs, or test suites. Only read and edit files.
Do NOT create new configuration files unless the ticket explicitly requires it.
If you need to look up API documentation or library usage, use the available search tools.
EOF
)

# Run Claude Code headless
echo "=== Running Claude Code ==="
claude --bare -p "$PROMPT" \
  --allowedTools "Read,Edit,Glob,Grep,mcp__context7__*,mcp__exa__*,mcp__chrome-devtools__*" \
  --mcp-config /etc/claude/mcp.json \
  --output-format json \
  --max-turns 50 \
  > "$OUTPUT_FILE" 2>&1 || {
    echo "=== Claude Code failed ==="
    linear_comment "$ISSUE_ID" "Agent failed to implement this ticket. Check Job logs."
    notify_telegram "Agent FAILED on ${ISSUE_IDENTIFIER}: ${ISSUE_TITLE}"
    exit 1
  }

RESULT=$(jq -r '.result // "No output"' "$OUTPUT_FILE" | head -100)

# Check if any files were changed
if git diff --quiet && git diff --cached --quiet; then
  echo "=== No changes made ==="
  linear_comment "$ISSUE_ID" "Agent ran but made no changes. Ticket may need more detail."
  notify_telegram "Agent made NO CHANGES on ${ISSUE_IDENTIFIER}"
  exit 0
fi

# Commit
git add -A
git commit -m "feat: ${ISSUE_TITLE}

Implements ${ISSUE_IDENTIFIER}.
Agent: Claude Code headless.

${RESULT}" || {
  echo "=== Commit failed ==="
  exit 1
}

# Push
git push origin "$BRANCH"

# Create PR
PR_URL=$(gh pr create \
  --repo "$TARGET_REPO" \
  --base main \
  --head "$BRANCH" \
  --title "${ISSUE_IDENTIFIER}: ${ISSUE_TITLE}" \
  --body "## Linear Ticket
[${ISSUE_IDENTIFIER}](https://linear.app/issue/${ISSUE_IDENTIFIER})

## Agent Summary
${RESULT}

---
_Implemented by Claude Code agent. Review before merging._")

echo "=== PR created: $PR_URL ==="

# Update Linear: move to "In Review", comment with PR link
linear_set_state "$ISSUE_ID" "${IN_REVIEW_STATE_ID:-}"
linear_comment "$ISSUE_ID" "PR opened: $PR_URL"

# Notify Telegram
notify_telegram "PR opened for ${ISSUE_IDENTIFIER}: [${ISSUE_TITLE}](${PR_URL})"

echo "=== Agent complete ==="
```

#### 3.2 MCP configuration

```json
// agent/mcp.json
{
  "mcpServers": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@context7/mcp-server"]
    },
    "exa": {
      "command": "npx",
      "args": ["-y", "@exa-ai/mcp-server"],
      "env": {
        "EXA_API_KEY": "${EXA_API_KEY}"
      }
    },
    "chrome-devtools": {
      "command": "npx",
      "args": ["-y", "chrome-devtools-mcp@latest", "--slim", "--headless"]
    }
  }
}
```

#### 3.3 Dockerfile

```dockerfile
# agent/Dockerfile
FROM node:22-slim

# System dependencies
RUN apt-get update && apt-get install -y \
    git \
    jq \
    curl \
    chromium \
    && rm -rf /var/lib/apt/lists/*

# GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
    && apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Pre-cache MCP server packages (reduces cold start)
RUN npx -y @context7/mcp-server --help || true
RUN npx -y @exa-ai/mcp-server --help || true
RUN npx -y chrome-devtools-mcp@latest --help || true

# Agent script and MCP config
COPY mcp.json /etc/claude/mcp.json
COPY ticket-agent.sh /usr/local/bin/ticket-agent.sh
RUN chmod +x /usr/local/bin/ticket-agent.sh

# Set Chromium path for puppeteer/chrome-devtools-mcp
ENV CHROME_PATH=/usr/bin/chromium
ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium

WORKDIR /home/agent
ENTRYPOINT ["/usr/local/bin/ticket-agent.sh"]
```

#### 3.4 Build and push

```bash
docker build -t ghcr.io/<org>/claude-agent:latest agent/
docker push ghcr.io/<org>/claude-agent:latest
```

### Deliverables

- [ ] `ticket-agent.sh` entrypoint
- [ ] `mcp.json` MCP server configuration
- [ ] `Dockerfile` for agent container
- [ ] Image pushed to GHCR
- [ ] Manual test: run container locally with a test ticket

---

## Phase 4: CI/CD Integration (Tekton + ArgoCD)

### Goal

Tekton runs CI on the PR. ArgoCD deploys on merge. Both report status via Telegram.

### Tasks

#### 4.1 Tekton: PR pipeline trigger

Assumes you already have Tekton Triggers installed. Add a GitHub webhook EventListener for PR events.

```yaml
# tekton/trigger-binding.yaml
apiVersion: triggers.tekton.dev/v1beta1
kind: TriggerBinding
metadata:
  name: github-pr-binding
  namespace: tekton-pipelines
spec:
  params:
    - name: repo-url
      value: $(body.pull_request.head.repo.clone_url)
    - name: revision
      value: $(body.pull_request.head.sha)
    - name: pr-number
      value: $(body.number)
    - name: repo-name
      value: $(body.repository.full_name)
---
apiVersion: triggers.tekton.dev/v1beta1
kind: TriggerTemplate
metadata:
  name: github-pr-template
  namespace: tekton-pipelines
spec:
  params:
    - name: repo-url
    - name: revision
    - name: pr-number
    - name: repo-name
  resourcetemplates:
    - apiVersion: tekton.dev/v1
      kind: PipelineRun
      metadata:
        generateName: pr-$(tt.params.pr-number)-
      spec:
        pipelineRef:
          name: pr-ci-pipeline # Your existing CI pipeline
        params:
          - name: repo-url
            value: $(tt.params.repo-url)
          - name: revision
            value: $(tt.params.revision)
          - name: pr-number
            value: $(tt.params.pr-number)
        workspaces:
          - name: source
            volumeClaimTemplate:
              spec:
                accessModes: ["ReadWriteOnce"]
                resources:
                  requests:
                    storage: 1Gi
```

#### 4.2 Tekton: pipeline definition (skeleton)

Adapt to your existing pipelines. This is a minimal example.

```yaml
# tekton/pipeline.yaml
apiVersion: tekton.dev/v1
kind: Pipeline
metadata:
  name: pr-ci-pipeline
  namespace: tekton-pipelines
spec:
  params:
    - name: repo-url
    - name: revision
    - name: pr-number
  workspaces:
    - name: source
  tasks:
    - name: clone
      taskRef:
        name: git-clone # from Tekton Hub
      params:
        - name: url
          value: $(params.repo-url)
        - name: revision
          value: $(params.revision)
      workspaces:
        - name: output
          workspace: source
    - name: lint-and-test
      runAfter: ["clone"]
      taskRef:
        name: repo-ci # your repo-specific CI task
      workspaces:
        - name: source
          workspace: source
    - name: build-image
      runAfter: ["lint-and-test"]
      taskRef:
        name: kaniko-build # build container image
      params:
        - name: IMAGE
          value: ghcr.io/<org>/$(params.repo-name):pr-$(params.pr-number)
      workspaces:
        - name: source
          workspace: source
  finally:
    - name: notify
      taskRef:
        name: telegram-notify # custom task
      params:
        - name: message
          value: "CI $(tasks.status) for PR #$(params.pr-number)"
```

#### 4.3 ArgoCD: auto-sync on merge

Your existing ArgoCD Applications already watch repos. No changes needed if your app-of-apps pattern is in place. On PR merge to `main`, ArgoCD detects the change and syncs.

To get Telegram notifications on sync:

```yaml
# argocd/notifications-cm.yaml (ArgoCD Notifications)
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-notifications-cm
  namespace: argocd
data:
  service.telegram: |
    token: $telegram-bot-token
  trigger.on-sync-succeeded: |
    - when: app.status.operationState.phase in ['Succeeded']
      send: [telegram-deployed]
  template.telegram-deployed: |
    message: |
      ✅ *{{ .app.metadata.name }}* deployed
      Revision: {{ .app.status.sync.revision | trunc 7 }}
```

### Deliverables

- [ ] Tekton TriggerBinding + TriggerTemplate for PR events
- [ ] CI Pipeline adapted per repo
- [ ] ArgoCD Notifications configured for Telegram
- [ ] End-to-end test: PR → pipeline → deploy → notification

---

## Phase 5: Telegram Notifications

### Goal

All pipeline events surface on your phone via Telegram.

### Events to notify

| Event            | Source               | Message                                    |
| ---------------- | -------------------- | ------------------------------------------ |
| Ticket picked up | Orchestrator         | "Agent picked up LIN-42: Add RBAC support" |
| Agent failed     | Agent container      | "Agent FAILED on LIN-42. Check logs."      |
| Agent no changes | Agent container      | "Agent made no changes on LIN-42."         |
| PR opened        | Agent container      | "PR #17 opened for LIN-42: [link]"         |
| CI passed/failed | Tekton finally       | "CI passed for PR #17"                     |
| Deployed         | ArgoCD Notifications | "homelab-apps deployed, rev abc1234"       |

### Implementation

Reuse your existing Telegram bot framework. Each component calls a shared `notify_telegram()` function or posts to a shared webhook endpoint that your bot listens on.

### Deliverables

- [ ] Telegram bot configured with dedicated agent-pipeline chat/channel
- [ ] All 6 event types wired

---

## Phase 6: Claude Chat → Linear Ticket Creation (Optional)

### Goal

Create and refine tickets directly from Claude chat on your phone.

### Options (pick one)

#### Option A: Linear MCP connector in Claude.ai

Check if Linear MCP is available in Claude.ai connectors. If yes, Claude chat can call `linear_create_issue` directly.

#### Option B: React artifact

Build a React artifact that calls the Linear GraphQL API from the browser. Claude pre-fills the form based on your conversation, you review and submit.

#### Option C: Telegram bot

Send a message to your Telegram bot describing the feature. The bot calls Claude API to structure it into a ticket, then creates it in Linear. Works outside Claude chat.

### Deliverables

- [ ] One of the above options implemented
- [ ] Test: create a ticket from phone → appears in Linear board

---

## Secrets Management

All secrets stored in OpenBao, injected into K8s via External Secrets Operator.

| Secret                    | Used by                        |
| ------------------------- | ------------------------------ |
| `anthropic-api-key`       | Agent container                |
| `github-token`            | Agent container (gh CLI)       |
| `linear-api-key`          | Orchestrator + Agent container |
| `linear-webhook-secret`   | Orchestrator                   |
| `telegram-bot-token`      | Orchestrator + Tekton + ArgoCD |
| `telegram-chat-id`        | Orchestrator + Tekton + ArgoCD |
| `exa-api-key`             | Agent container (MCP)          |
| `cloudflare-tunnel-token` | Cloudflared (webhook exposure) |

```yaml
# externalsecret.yaml (ESO → OpenBao)
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: agent-secrets
  namespace: agent-pipeline
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: openbao
    kind: ClusterSecretStore
  target:
    name: agent-secrets
  data:
    - secretKey: anthropic-api-key
      remoteRef:
        key: secret/data/agent-pipeline
        property: anthropic-api-key
    - secretKey: github-token
      remoteRef:
        key: secret/data/agent-pipeline
        property: github-token
    - secretKey: linear-api-key
      remoteRef:
        key: secret/data/agent-pipeline
        property: linear-api-key
    - secretKey: linear-webhook-secret
      remoteRef:
        key: secret/data/agent-pipeline
        property: linear-webhook-secret
    - secretKey: telegram-bot-token
      remoteRef:
        key: secret/data/agent-pipeline
        property: telegram-bot-token
    - secretKey: telegram-chat-id
      remoteRef:
        key: secret/data/agent-pipeline
        property: telegram-chat-id
    - secretKey: exa-api-key
      remoteRef:
        key: secret/data/agent-pipeline
        property: exa-api-key
    - secretKey: cloudflare-tunnel-token
      remoteRef:
        key: secret/data/agent-pipeline
        property: cloudflare-tunnel-token
```

---

## Repository Structure

```
agent-pipeline/
├── orchestrator/
│   ├── main.py
│   ├── Dockerfile
│   ├── requirements.txt        # fastapi, httpx, kubernetes
│   └── k8s/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── rbac.yaml
│       └── cloudflared.yaml
├── agent/
│   ├── ticket-agent.sh
│   ├── mcp.json
│   └── Dockerfile
├── tekton/
│   ├── trigger-binding.yaml
│   ├── trigger-template.yaml
│   └── pipeline.yaml
├── argocd/
│   ├── notifications-cm.yaml
│   └── application.yaml        # app-of-apps entry for this project
├── secrets/
│   └── externalsecret.yaml
└── README.md
```

---

## Implementation Order

| Step | Phase                                                        | Effort    | Dependencies          |
| ---- | ------------------------------------------------------------ | --------- | --------------------- |
| 1    | Linear setup (1.1–1.4)                                       | 1 hour    | None                  |
| 2    | Agent container (3.1–3.4)                                    | 3–4 hours | Linear API key        |
| 3    | Manual test: run agent container locally against a test repo | 1 hour    | Step 2                |
| 4    | Orchestrator service (2.1–2.3)                               | 3–4 hours | Step 1, Step 2        |
| 5    | K8s deployment (orchestrator + RBAC + secrets)               | 2 hours   | Step 4, OpenBao       |
| 6    | Webhook exposure (tunnel)                                    | 1 hour    | Step 5                |
| 7    | End-to-end test: Linear → orchestrator → agent → PR          | 1–2 hours | Steps 1–6             |
| 8    | Tekton CI triggers (4.1–4.2)                                 | 2–3 hours | Existing Tekton setup |
| 9    | ArgoCD notifications (4.3)                                   | 1 hour    | Existing ArgoCD setup |
| 10   | Telegram wiring (Phase 5)                                    | 1–2 hours | Existing Telegram bot |
| 11   | Claude Chat ticket creation (Phase 6)                        | 2–3 hours | Optional              |

**Estimated total: 2–3 weekends of focused work.**

---

## Risk Mitigations

| Risk                              | Mitigation                                                                           |
| --------------------------------- | ------------------------------------------------------------------------------------ |
| Claude produces bad code          | Human reviews every PR before merge. Never auto-merge.                               |
| Agent runs forever / burns tokens | `--max-turns 50` on Claude Code. K8s Job `activeDeadlineSeconds: 600`.               |
| Vague tickets produce vague code  | Enforce ticket template. Add validation in orchestrator (reject tickets without AC). |
| MCP servers fail in container     | Pre-cache npx packages in Dockerfile. Add healthcheck/fallback.                      |
| Webhook endpoint compromised      | Verify Linear webhook signature. Rate limit. Cloudflare Tunnel (no open ports).      |
| API key leaks                     | OpenBao + ESO. Never in git. K8s Secrets only via SecretKeyRef.                      |
| Agent modifies wrong files        | `Read,Edit` only (no Bash). Claude Code sandboxed to cloned repo.                    |

---

## Future Extensions

- **Multi-agent**: orchestrator spawns parallel Jobs for independent tickets
- **Session memory**: store agent outputs in a DB, feed relevant history into future prompts
- **Auto-triage**: Claude analyzes new tickets and suggests refinements before "Ready" status
- **Cost tracking**: log API token usage per ticket, surface in Telegram summary
- **Agent-agnostic**: swap Claude Code for OpenHands, Aider, or future alternatives via config
- **PR review agent**: second agent reviews PRs opened by the first agent before human review
