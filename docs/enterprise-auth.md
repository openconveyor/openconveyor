# Enterprise auth: short-lived credentials for agents

The weakest credential in an agent pipeline is usually a long-lived
personal access token sitting in a cluster Secret. This page documents
the recommended alternative: mint short-lived, tightly-scoped tokens
*outside* the Task, and let OpenConveyor's secrets-as-files contract
deliver them unchanged.

Nothing here requires changes to OpenConveyor. It composes three
existing pieces:

1. OpenConveyor projects declared Secrets as read-only files at
   `/run/secrets/<name>/<key>` (ADR-0004). Agents read them **once, at
   Job start** — so a Secret whose value rotates underneath a running
   Task is harmless, and a token that expires shortly *after* the Task's
   timeout is ideal.
2. [External Secrets Operator](https://external-secrets.io) (ESO)
   keeps cluster Secrets in sync with an external backend and can run
   **generators** that mint fresh credentials on every refresh.
3. GitHub Apps issue installation tokens that expire after 60 minutes
   and are scoped to only the repositories the App is installed on.

## The pattern: GitHub App installation tokens via ESO

Instead of storing a PAT, store the GitHub App's **private key** in
your secret backend. ESO's `GithubAccessToken` generator exchanges it
for a fresh installation token on every refresh; the resulting `Secret`
keeps the exact name and key the reference agents expect
(`github-token` / `token` → `/run/secrets/github-token/token` →
`$GITHUB_TOKEN`).

```yaml
# 1. Mirror the App private key (plaintext PEM) from your backend.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: github-app-private-key
  namespace: conveyor-tasks
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: <your-secret-store>        # ClusterSecretStore or SecretStore
    kind: ClusterSecretStore
  target:
    name: github-app-private-key
    creationPolicy: Owner
  data:
    - secretKey: privateKey
      remoteRef:
        key: <path/to/github-app-private-key>   # plaintext PEM, no property selector
---
# 2. Generator: exchanges the private key for a 60m installation token.
apiVersion: generators.external-secrets.io/v1alpha1
kind: GithubAccessToken
metadata:
  name: github-app-token
  namespace: conveyor-tasks
spec:
  appID: "<your-app-id>"
  installID: "<your-installation-id>"
  auth:
    privateKey:
      secretRef:
        name: github-app-private-key
        key: privateKey
---
# 3. The Secret the agents actually mount. Name and key are load-bearing:
#    the reference agents discover /run/secrets/github-token/token and
#    export it as $GITHUB_TOKEN.
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: github-token
  namespace: conveyor-tasks
spec:
  refreshInterval: 20m
  target:
    name: github-token
    creationPolicy: Owner
  dataFrom:
    # The generator emits key "token", preserving the expected path.
    - sourceRef:
        generatorRef:
          apiVersion: generators.external-secrets.io/v1alpha1
          kind: GithubAccessToken
          name: github-app-token
```

A Task then declares the Secret exactly as before:

```yaml
spec:
  permissions:
    secrets:
      - github-token
```

## The timing invariant

Agents read the token once at Job start, so the token must outlive the
longest Task that can start with it. Choose values so that:

```
refreshInterval + max Task timeout < token TTL
```

With the defaults above: a token is at most 20 minutes old when a Task
starts, leaving ≥ 40 minutes of validity — comfortably above a 30-minute
`spec.resources.timeout`. If you raise your Task timeouts, shorten the
refresh interval (or vice versa) and re-check the sum. GitHub App
installation token TTL is fixed at 60 minutes.

## Why this beats a PAT

- **Blast radius**: a leaked installation token dies within the hour
  and only reaches the repositories the App is installed on. A leaked
  PAT is the user's whole account until someone notices.
- **Scoping**: App permissions are declared once, centrally
  (contents, pull requests, issues — whatever your agents need), not
  inherited from a human's role.
- **Audit**: API activity shows up as the App, not as a person, so
  agent actions are separable in audit logs.
- **Zero agent changes**: the reference agents are oblivious — the
  file path and env var are identical to the static-secret setup.

## Other backends, same shape

The pattern generalizes to any credential a controller can mint outside
the Task: an ESO generator or Vault dynamic-secrets engine producing
short-lived database credentials, cloud STS tokens, or registry
credentials all land the same way — a rotating Secret with a stable
name, projected read-only into the Job. The invariant above is the only
thing to re-derive per credential type.

## See also

- [`security-model.md`](security-model.md) — guarantee 3, declared
  secrets only.
- [`adr/0004-secrets-as-files.md`](adr/0004-secrets-as-files.md) — why
  secrets are files, never env vars.
- [ESO `GithubAccessToken` generator docs](https://external-secrets.io/latest/api/generator/github/)
