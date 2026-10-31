# Tailscale Funnel to a kind-hosted trigger adapter

Resolves wayfinder ticket #102 (map #98). Researched 2026-10-31 from the
Tailscale docs (Funnel, Serve, HTTPS, ACL syntax, Kubernetes operator), the
Tailscale source at tag v1.102.5 (the `tailscaled` version on this PC), the
kind docs, the GitHub webhook docs, and this repo's code. The research only
ran read-only commands on this PC (`tailscale status`, `tailscale funnel
status`, `kind version`, `docker info`). No funnel ran and no config
changed, so the command sequence below is **unverified** end to end.

## Summary of answers

1. **Use one Funnel to a localhost port, and feed that port from kind
   with `extraPortMappings` + a NodePort Service.** The Funnel reverse
   proxy sends traffic only to a target that `tailscaled` can reach from
   the host, and the Serve docs say that proxies support only
   `http://127.0.0.1` ([Serve docs](https://tailscale.com/kb/1242/tailscale-serve)).
   kind has no command that changes the port mappings of a running
   cluster, so the mapping goes into the kind config before
   `kind create cluster`.
   `kubectl port-forward` also gives a localhost port, but it stops when
   the Pod restarts, so it is a debugging tool, not a deployment.
2. **The public URL is `https://cachyos-x8664.tail1ddea3.ts.net/`** on port
   443 (or `:8443` / `:10000`). The hostname is fixed by the machine name
   and the tailnet name. `tailscaled` on this PC terminates TLS with a
   Let's Encrypt certificate that Tailscale provisions. The Funnel relay
   does not decrypt the traffic ([Funnel docs](https://tailscale.com/kb/1223/funnel)).
3. **One Funnel serves `/github/issues` and `/github/pull-request`.** Mount
   the adapter at `/` (the default). The adapter routes by
   `ClusterTriggerClass.spec.path` with an exact match
   (`internal/trigger/handler.go:177-202`). Do not use `--set-path=/github`:
   Serve strips the mount prefix before it forwards the request
   (`ipn/ipnlocal/serve.go`, `http.StripPrefix`), and the adapter would see
   `/issues`.
4. **Limits.** GitHub caps a payload at 25 MB and wants a 2XX within 10
   seconds. The adapter caps the body at 1 MiB (ADR-0006) and answers in
   one request. Funnel adds a "non-configurable bandwidth limit" with no
   published number and no documented request timeout. Funnel listens
   only on 443, 8443 and 10000, only over TLS.
5. **`--bg` is required for persistence.** Without `--bg` the Funnel stops
   when the terminal closes. With `--bg`, `tailscaled` stores the serve
   config in its state file and restores it after a reboot or `tailscale
   down` / `tailscale up`.
6. **Tailnet prerequisites.** MagicDNS on (done), HTTPS certificates on
   (done: this node has the `https` capability), and the `funnel` node
   attribute in the policy file (**not done**: this node has no `funnel`
   capability). The first `tailscale funnel` run opens a browser page
   where an Owner approves it. This account is Owner and Admin.
7. **HMAC still protects the endpoint.** Funnel changes nothing in the
   trust boundary of ADR-0006. The relay forwards the raw body and the
   `X-Hub-Signature-256` header unchanged. The adapter verifies
   `HMAC-SHA256(secret, rawBody)` in constant time, so a stranger who
   finds the public URL gets 401. Funnel adds `Tailscale-Funnel-Request:
   ?1` and `X-Forwarded-For` to each request.

## Local state on 2026-10-31

| Item | Value | Source |
|---|---|---|
| Tailscale CLI | 1.104.4 | `tailscale version` |
| `tailscaled` | 1.102.5 (CLI warns: `client version != tailscaled server version`) | `tailscale status` |
| `tailscaled` service | enabled, active | `systemctl` |
| Node DNS name | `cachyos-x8664.tail1ddea3.ts.net` | `tailscale status --json` `.Self.DNSName` |
| MagicDNS | enabled | `.CurrentTailnet.MagicDNSEnabled` |
| Node capabilities | `https`, `is-admin`, `is-owner`, `ssh`, `file-sharing`; **no `funnel`** | `.Self.CapMap` |
| Serve / Funnel config | `No serve config` | `tailscale funnel status`, `tailscale serve status` |
| Health warnings | systemd-resolved and NetworkManager wired incorrectly (MagicDNS on this host); peers advertise routes but `--accept-routes` is false | `tailscale status` |
| kind | v0.33.0 (default node image is k8s 1.37; `Makefile:70` pins `kindest/node:v1.36.4`) | `kind version` |
| kind clusters | none | `kind get clusters` |
| Docker | 29.8.2, rootful (`cgroupns`, cgroup driver `systemd`), `kind` bridge network exists | `docker info` |
| Local listeners on 443, 8443, 9090, 10000 | none | `ss -ltn` |
| kube contexts | only `tailscale-operator.tail1ddea3.ts.net` (the operator lives in another cluster on the tailnet, not in kind) | `kubectl config get-contexts` |

The CLI and daemon version mismatch does not block Funnel, but
`sudo systemctl restart tailscaled` (not run) brings them in sync. The
MagicDNS warning affects outbound name resolution on this PC. It does not
affect inbound Funnel traffic, because public DNS resolves the Funnel
hostname to a Tailscale relay.

## Three ways to give `tailscaled` a localhost port

`tailscale funnel <target>` is a reverse proxy inside `tailscaled`. The
Serve docs state: "Reverse proxies only support `http://127.0.0.1`"
([Serve docs](https://tailscale.com/kb/1242/tailscale-serve)). The
`ipn.ExpandProxyTargetValue` code at v1.102.5 also accepts another host
when the target has a scheme, so a kind node IP such as
`http://172.18.0.2:30090` may work (**unverified**; the docs do not promise
it, and the node IP changes when kind recreates the cluster). The options
below all give a stable `127.0.0.1:<port>`.

| Option | How the port appears on the host | Survives Pod restart | Survives reboot | Verdict |
|---|---|---|---|---|
| kind `extraPortMappings` + NodePort Service | Docker publishes `hostPort` on `listenAddress` to the node container; kube-proxy maps the NodePort to the Pod ([kind configuration](https://kind.sigs.k8s.io/docs/user/configuration/)) | yes | yes (Docker restarts the node container) | **Recommended.** Needs the mapping in the kind config at create time. |
| `kubectl port-forward` | A `kubectl` process holds a localhost socket and tunnels through the API server | no, the process exits when the Pod goes away | no | Debugging only. |
| LoadBalancer with `cloud-provider-kind` | A host binary creates a load balancer container per Service ([kind loadbalancer](https://kind.sigs.k8s.io/docs/user/loadbalancer/)) | yes | needs the binary to run again | More moving parts than NodePort for one port. |
| Tailscale Kubernetes operator Ingress with `tailscale.com/funnel: "true"` | A proxy Pod in the cluster joins the tailnet and runs Funnel itself ([operator Funnel docs](https://tailscale.com/docs/kubernetes-operator/ingress/expose-workload-to-internet)) | yes | yes | Clean, but a different hostname (`<tls-host>.tail1ddea3.ts.net`), a second tailnet node, and the `funnel` attribute for `tag:k8s`. Out of scope for "from this PC". |

Linux rootful Docker (this PC) can reach the kind node IP directly
without port mappings ([kind configuration](https://kind.sigs.k8s.io/docs/user/configuration/)),
but see the localhost note above.

## Recommended command sequence

Values: cluster `conveyor-dev`, host port `9090`, NodePort `30090`,
namespace `conveyor-system`, Deployment `conveyor-controller-manager`
(`config/default/kustomization.yaml` sets `namePrefix: conveyor-` and
`namespace: conveyor-system`).

### 1. Create the kind cluster with the port mapping

`kind-funnel.yaml`:

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      # kind docs: "To use port mappings with NodePort, the kind node
      # containerPort and the service nodePort needs to be equal."
      - containerPort: 30090
        hostPort: 9090
        listenAddress: "127.0.0.1"   # default is 0.0.0.0; keep it local
        protocol: TCP
```

```sh
kind create cluster --name conveyor-dev \
  --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed \
  --config kind-funnel.yaml
kubectl config use-context kind-conveyor-dev
```

The `--image` value is `KIND_NODE_IMAGE` from `Makefile:70`. Use it,
because kind v0.33.0 defaults to Kubernetes 1.37.

### 2. Deploy the controller with the trigger adapter enabled

```sh
make docker-build IMG=controller:dev
kind load docker-image controller:dev --name conveyor-dev
make install
make deploy IMG=controller:dev
kubectl -n conveyor-system patch deployment conveyor-controller-manager --type=json -p='[
  {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--trigger-bind-address=:9090"},
  {"op":"add","path":"/spec/template/spec/containers/0/ports/-","value":{"name":"trigger","containerPort":9090,"protocol":"TCP"}}
]'
```

`config/manager/manager.yaml:63-66` has no trigger flag and `ports: []`,
so the patch adds both. A kustomize overlay is the durable form of this
patch. `--trigger-namespace` defaults to `conveyor-system`
(`cmd/main.go:58`), which is where the HMAC Secret must live.

### 3. Expose the adapter on the NodePort

```yaml
apiVersion: v1
kind: Service
metadata:
  name: conveyor-trigger
  namespace: conveyor-system
spec:
  type: NodePort
  selector:
    control-plane: controller-manager
    app.kubernetes.io/name: conveyor
  ports:
    - name: trigger
      port: 9090
      targetPort: 9090
      nodePort: 30090
      protocol: TCP
```

```sh
kubectl apply -f conveyor-trigger-service.yaml
curl -i http://127.0.0.1:9090/healthz        # expect HTTP/1.1 200 OK
```

The selector labels come from `config/manager/manager.yaml:30-31`. The
adapter answers `/healthz` with 200 (`internal/trigger/server.go:52`).

### 4. Create the webhook Secret and the ClusterTriggerClasses

```sh
openssl rand -hex 32 > /tmp/gh-webhook-secret           # >= 32 bytes of entropy (ADR-0006)
kubectl -n conveyor-system create secret generic github-webhook \
  --from-file=secret=/tmp/gh-webhook-secret
```

Create one `ClusterTriggerClass` per path. Start from
`config/samples/conveyor_v1alpha1_clustertriggerclass.yaml` and set
`spec.path: /github/issues` and `spec.path: /github/pull-request`. Both
can reference the same Secret or different Secrets; GitHub lets each
webhook have its own secret.

### 5. Start the Funnel

```sh
tailscale funnel --bg --https=443 http://127.0.0.1:9090
```

Expected output (format from the Funnel docs):

```
Available on the internet:

https://cachyos-x8664.tail1ddea3.ts.net/
|-- / proxy http://127.0.0.1:9090

Funnel started and running in the background.
```

The first run on this tailnet prints a URL to approve Funnel in the admin
console and waits ("Success." after the `funnel` capability arrives).
Later runs skip that step. `--https=443` is the default; use
`--https=8443` to keep 443 free, which puts `:8443` in the public URL.

Checks:

```sh
tailscale funnel status
curl -i https://cachyos-x8664.tail1ddea3.ts.net/healthz                     # 200
curl -i -X POST https://cachyos-x8664.tail1ddea3.ts.net/github/issues -d '{}'  # 401 missing signature header
```

Public DNS for a new Funnel hostname can take up to 10 minutes
([Funnel docs](https://tailscale.com/kb/1223/funnel)).

### 6. Register the webhooks in GitHub

Repository → Settings → Webhooks → Add webhook
([Creating webhooks](https://docs.github.com/en/webhooks/using-webhooks/creating-webhooks)):

| Field | Value |
|---|---|
| Payload URL | `https://cachyos-x8664.tail1ddea3.ts.net/github/issues` (second webhook: `/github/pull-request`) |
| Content type | `application/json` (required: the adapter reads the body as JSON; the form variant wraps it in a `payload` field) |
| Secret | the value from `/tmp/gh-webhook-secret` |
| SSL verification | leave enabled; the Let's Encrypt certificate is valid |
| Events | `Issues` for the first webhook, `Pull requests` for the second |

GitHub sends a `ping` event on creation. The adapter answers 202 when the
filters do not match, so a 202 on `ping` is the success signal.

### 7. Stop or remove

```sh
tailscale funnel --https=443 off      # remove this one handler; all original flags are required
tailscale funnel reset                # clear the whole serve/funnel config
```

## Hostname and certificate

- The Funnel hostname is `<machine-name>.<tailnet-name>.ts.net`; the
  tailnet name is `tail1ddea3.ts.net` and the machine name is
  `cachyos-x8664`. Funnel "can only use DNS names in your tailnet's
  domain" ([Funnel docs](https://tailscale.com/kb/1223/funnel)). A custom
  domain is not possible.
- Public DNS resolves that name to a Funnel relay, which opens a TCP proxy
  to this PC over the tailnet. `tailscaled` on this PC terminates TLS and
  forwards plain HTTP to `127.0.0.1:9090` (same source).
- Tailscale requests the certificate from Let's Encrypt when HTTPS is
  enabled for the tailnet. The certificate lands in Certificate
  Transparency logs, so the machine name is public. Rename the machine
  first when the name is sensitive ([HTTPS docs](https://tailscale.com/kb/1153/enabling-https)).
  `tailscaled` renews the certificate for Serve/Funnel; the 90-day manual
  renewal applies only to `tailscale cert` files.
- Frequent certificate requests hit Let's Encrypt rate limits; the docs
  warn of a wait of "34 hours".

## Path routing

- Serve stores web handlers per `host:port`, keyed by mount path. A
  request matches the exact path first, then the longest parent directory
  (`getServeHandler` in `ipn/ipnlocal/serve.go` at v1.102.5).
- A proxy handler strips the mount prefix:
  `http.StripPrefix(strings.TrimSuffix(mountPoint, "/"), h)`. With the
  default mount `/` nothing is stripped, so `/github/issues` and
  `/github/pull-request` reach the adapter unchanged.
- The adapter trims a trailing slash and then needs an exact match on
  `spec.path` (`internal/trigger/handler.go:178-192`). Two
  `ClusterTriggerClass` objects with distinct paths are the right shape;
  two with the same path produce a 500 (`:202`).
- One Funnel per port. A second `tailscale funnel --set-path=...` on the
  same port adds a mount instead of a new URL. Only ports 443, 8443 and
  10000 give extra public URLs.

## Limits

| Limit | Value | Source |
|---|---|---|
| GitHub payload size | "Payloads are capped at 25 MB"; larger events are not delivered | [Webhook events and payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads) |
| Adapter body cap | 1 MiB, response 413 | `internal/trigger/handler.go:32`, ADR-0006 |
| GitHub response deadline | 2XX "within 10 seconds", otherwise the delivery fails | [Best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks) |
| Adapter server timeouts | `ReadHeaderTimeout` 5 s, `ReadTimeout` 30 s, `WriteTimeout` 30 s, `IdleTimeout` 60 s | `internal/trigger/server.go:58-61` |
| Funnel request timeout | none documented; the `http.Server` in `ipn/ipnlocal/serve.go` sets no read/write timeouts; proxy transport `IdleConnTimeout` 90 s, `TLSHandshakeTimeout` 10 s | source at v1.102.5 |
| Funnel bandwidth | "subject to non-configurable bandwidth limits"; no number published; Tailscale status page lists Funnel performance incidents | [Funnel docs](https://tailscale.com/docs/features/tailscale-funnel) |
| Funnel ports | 443, 8443, 10000 (the allowed list comes from the `funnel-ports` node capability, `ipn.CheckFunnelPort`) | [Funnel docs](https://tailscale.com/kb/1223/funnel), source |
| Protocol | TLS only; HTTP without TLS is not possible | [Funnel docs](https://tailscale.com/kb/1223/funnel) |
| Serve and Funnel on one port | the last command wins: `serve` makes the port private, `funnel` makes it public | [Funnel docs](https://tailscale.com/docs/features/tailscale-funnel) |
| Redelivery | GitHub does not retry on its own; use "Redeliver" in Recent Deliveries after an outage | [Best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks) |

The adapter creates the Task inside the request and then answers, so the
GitHub deadline holds when the API server answers in time. A reboot of
this PC drops deliveries during the outage; GitHub keeps them in Recent
Deliveries for manual redelivery.

## Persistence

- `--bg`: "Funnel runs persistently in the background until you" turn it
  off, and "automatically resumes after a reboot" or `tailscale down` /
  `tailscale up` ([Funnel CLI](https://tailscale.com/kb/1311/tailscale-funnel)).
- Mechanism: the CLI writes a `ServeConfig`; `tailscaled` saves it as
  JSON under `ipn.ServeConfigKey(profileID)` in its state store and
  reloads it on start (`setServeConfigLocked` / `reloadServeConfigLocked`
  at v1.102.5). `tailscaled` is `enabled` in systemd on this PC, so the
  Funnel comes back with the service.
- Without `--bg`: the config lives in `ServeConfig.Foreground[sessionID]`
  for the life of the IPN bus watch. Ctrl-C or a closed terminal removes
  it.
- The kind side: kind starts the node container with a Docker restart
  policy (`on-failure`), so Docker normally restarts
  `conveyor-dev-control-plane` after a reboot and the hostPort mapping
  returns with it (**unverified** on this PC). The Pod starts after the
  node is Ready, so the Funnel answers 502 until then.

## Tailnet admin prerequisites

All three are tailnet-wide settings in the admin console
([Funnel docs](https://tailscale.com/kb/1223/funnel)):

1. **MagicDNS enabled.** Done (`MagicDNSEnabled: true`).
2. **HTTPS certificates enabled.** DNS page → HTTPS Certificates →
   Enable HTTPS. Done (this node has the `https` capability).
3. **`funnel` node attribute in the policy file.** Not done (no `funnel`
   capability on this node). Either accept the approval page that the
   first `tailscale funnel` run opens, use Access controls → Funnel → "Add
   Funnel to policy", or add it by hand:

   ```json
   "nodeAttrs": [
     { "target": ["autogroup:member"], "attr": ["funnel"] }
   ]
   ```

   A narrower target such as `["martijn.scholten@..."]` or a tag also
   works. `autogroup:member` does not cover tagged devices, so the
   Kubernetes operator route needs `tag:k8s` in the target.

Owners, Admins and Network admins can edit the policy. This account has
`is-owner` and `is-admin`.

## How the HMAC secret still protects the endpoint

- The Funnel relay is a TCP proxy; it cannot read or change the request.
  `tailscaled` on this PC decrypts TLS and forwards the request with the
  same body and headers. The `X-Hub-Signature-256` header arrives
  unchanged, and `HMAC(secret, rawBody)` still matches.
- `VerifyHMAC` compares in constant time and returns 401 on a missing or
  wrong signature (`internal/trigger/signature.go`). A stranger who
  scans `*.tail1ddea3.ts.net` gets 404 on an unknown path and 401 on a
  configured path. Neither response carries internal detail
  (`internal/trigger/handler.go:91-114`).
- `tailscaled` adds `X-Forwarded-For` (the public client IP),
  `X-Forwarded-Host`, `X-Forwarded-Proto: https`, and
  `Tailscale-Funnel-Request: ?1` to each request
  (`addProxyForwardedHeaders` in `ipn/ipnlocal/serve.go`). It does not add
  `Tailscale-User-*` identity headers for Funnel requests. The adapter
  ignores all of them, so none of them can grant access. The
  `r.RemoteAddr` in the adapter log is `127.0.0.1`, not the GitHub IP.
- The ADR-0006 consequences stay valid: a `ClusterTriggerClass` without
  filters is dangerous, and the secret needs >= 32 bytes of entropy. Funnel
  makes the endpoint reachable from the whole internet, so these two rules
  become the only gate.

## Open items

- The full sequence has not run on this PC (**unverified**). The first
  `tailscale funnel` run needs an Owner in a browser.
- Whether `tailscaled` 1.102.5 accepts a non-localhost proxy target such as
  `http://172.18.0.2:30090` is **unverified**; the docs say no.
- Funnel bandwidth and timeout numbers are not published. A webhook
  payload is small (GitHub payloads are typically far below 1 MiB), so
  the bandwidth limit is unlikely to matter.
- A kustomize overlay for `--trigger-bind-address` and the NodePort
  Service does not exist in the repo yet. The patch in step 2 is a
  one-off; a `config/trigger/` component is the durable form.
