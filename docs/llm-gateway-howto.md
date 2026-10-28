# How to: an LLM gateway demo on BNK 2.4.1 with ocibnkctl

A self-contained demo of the **AI/LLM gateway in BIG-IP Next for Kubernetes
(BNK) 2.4.1**, on one machine with Docker. No GPU, no LLM API key, no access
to anything beyond F5's normal image registry. Three paths:

- **Manual with ocibnkctl:** five commands, about 30 minutes the first time.
- **Agent-assisted:** a CLI coding agent (Claude Code, or any agent that reads
  `AGENTS.md`) runs and explains it for you.
- **On your own BNK 2.4.1 cluster, without ocibnkctl:** the changes to make
  by hand on a cluster installed with FLO.

What the demo shows, all through **TMM**, F5's data plane:

| | |
|---|---|
| **Virtual keys** | Applications get their own key; TMM checks it on every request and never forwards it to the provider. |
| **Model scope** | A key only reaches the models it is allowed; anything else is refused (403). |
| **One endpoint, any provider** | Clients speak the OpenAI API to one address; the gateway holds the real provider key. |
| **Metering** | Every request, refused ones too, lands in ClickHouse with tokens, cost and latency. |
| **Console** | A web UI per tenant: providers, models, routes, keys, spend and usage. |

> **Status of the feature.** The AI gateway ships in BNK 2.4.1 and is installed
> by the F5 Lifecycle Operator (FLO) from one CNEInstance field. The public BNK
> 2.4 documentation covers 2.4.0 and does **not** yet describe the AI gateway
> (see [References](#references)). Treat this as a preview of a shipped
> capability, not as the documented product procedure.

## Contents

- [What gets deployed](#what-gets-deployed)
- [Before you start](#before-you-start)
- [Path 1: manual](#path-1-manual)
- [Path 2: agent-assisted](#path-2-agent-assisted)
- [Path 3: your own BNK cluster, without ocibnkctl](#path-3-your-own-bnk-cluster-without-ocibnkctl)
- [Configure with kubectl](#configure-with-kubectl)
- [Open the console](#open-the-console)
- [Tenants and accounts](#tenants-and-accounts)
- [Send your own requests](#send-your-own-requests)
- [Use a real provider](#use-a-real-provider)
- [Troubleshooting](#troubleshooting)
- [Limits in 2.4.1](#limits-in-241)
- [Tear down](#tear-down)
- [References](#references)

## What gets deployed

```
  client (edge)     k3s cluster (Docker containers), BNK 2.4.1 via FLO
 ┌──────────────┐  ┌──────────────────────────────────────────────────────────────────┐
 │ bnk-edge-frr │  │ TMM: virtual key, model scope, meter ───> mock-llm               │
 │  curl ───────┼──│> 203.0.113.140:80                (OpenAI API, no GPU)            │
 │   over BGP   │  │   ^ config          │ one record per request                     │
 └──────────────┘  │   │                 v                                            │
                   │ f5-cne-controller              ClickHouse                        │
                   │ (AI gateway API)                                                 │
                   │   ^                                                              │
                   │ console "Polaris AI" :3000 <── port-forward <── you              │
                   └──────────────────────────────────────────────────────────────────┘
```

- **`ocibnkctl`** builds a k3s cluster out of Docker containers (one control
  node, one TMM node), installs BNK 2.4.1 with FLO, and turns on
  `CNEInstance.spec.aiGateway`. FLO then installs the console, the AI gateway
  API, a DNS resolver for provider hostnames, ClickHouse and the log receiver
  — all from F5's 2.4.1 release images on `repo.f5.com`.
- **The `llm-gateway` scenario** adds a mock LLM
  ([llm-d-inference-sim](https://github.com/llm-d/llm-d-inference-sim), an
  OpenAI-compatible simulator) and configures the gateway through its REST
  API, the same calls the console makes: tenant `demo`, a provider key for
  the mock, model `gpt-4o-mini`, a route on `203.0.113.140`, and a virtual key.
- **The client** is a container on the "edge" network, outside the cluster,
  that reaches the gateway address over BGP, as a real client would through a
  router.

## Before you start

**On a MacBook (Apple silicon, Docker Desktop)?** Follow
[llm-gateway-macbook.md](llm-gateway-macbook.md): Docker memory, the
Netskope CA, `console.sh`, sign-in and `ask.sh`, step by step with measured
numbers.

| Need | Detail |
|---|---|
| Machine | Linux (x86_64 or arm64) or macOS (Apple silicon) with Docker. **10 cores, 24 GB** for Docker (macOS: the Docker Desktop VM). Smaller hosts work with the small-host profile, slower — see [Troubleshooting](#troubleshooting). |
| Tools | `docker`, `kubectl`, `helm`. `ocibnkctl doctor` checks them and prints install commands. |
| F5 credentials | `f5-far-auth-key.tgz` (FAR image-pull key for `repo.f5.com`) and a BNK license JWT, from your usual F5 license/portal channel. They never leave your machine. |
| ocibnkctl | v2.4.1-1 or later: the [release binaries](https://github.com/mwiget/ocibnkctl/releases) (`darwin_arm64`, `linux_amd64`, `linux_arm64`), or `make install` from a clone. |

```bash
V=2.4.1-1                                   # this guide's release
OS=$(uname -s | tr A-Z a-z); ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -sSL "https://github.com/mwiget/ocibnkctl/releases/download/v$V/ocibnkctl_${V}_${OS}_${ARCH}.tar.gz" | tar xz ocibnkctl
install -m 0755 ocibnkctl ~/.local/bin/     # or: sudo install ocibnkctl /usr/local/bin/
ocibnkctl version
```

## Path 1: manual

```bash
ocibnkctl init llmdemo --profile llm-gateway   # 1. a PoC directory, set up for this demo
cd llmdemo
cp /path/to/f5-far-auth-key.tgz keys/          # 2. your F5 credentials (gitignored)
cp /path/to/license.jwt keys/.jwt
ocibnkctl doctor                               # 3. host check
ocibnkctl validate
ocibnkctl e2e --yolo --confirm-cluster llmdemo --with-scenarios   # 4. deploy + prove
```

`--profile llm-gateway` writes three things into `poc.yaml`, which you can
read and change before step 4:

```yaml
bnk:
  tmm_dataplane_mode: anycast-bgp   # clients reach the gateway address over BGP
  ai_gateway:
    enabled: true                   # → CNEInstance.spec.aiGateway
    ai_metrics: true                # ClickHouse + the log receiver
scenarios: [bgp-peer-frr, llm-gateway]   # what e2e --with-scenarios runs
```

`e2e` creates the cluster, installs BNK, then runs `bgp-peer-frr` and
`llm-gateway`. A green run ends with:

```
OK   TMM has the demo-llm virtual server on 203.0.113.140:80
OK   edge FRR learned 203.0.113.140/32 from TMM over BGP
OK   virtual key + allowed model → 200 with a usage block
OK   wrong key → 401
OK   no key → 401
OK   model outside the key's scope → 403
OK   ClickHouse otel.aigw_requests has records for tenant demo (200, 401, 403)
```

followed by a **Try it yourself** block with the exact commands for your PoC.
The profile also puts four scripts in the PoC directory: `console.sh`,
`demo-password.sh`, `admin-password.sh` ([Open the console](#open-the-console))
and `ask.sh` ([Send your own requests](#send-your-own-requests)).
Every run writes `reports/<timestamp>/run-llmdemo-<stamp>.md` and a JSON copy.

Re-run just the demo on a deployed cluster (about 15 seconds):

```bash
ocibnkctl scenario run llm-gateway
```

## Path 2: agent-assisted

Every PoC directory carries an `AGENTS.md` (and a `CLAUDE.md` that includes
it): the operating manual for an agent, with an **LLM gateway demo** section
that says what to run, what to check, what to report and which files are
secret. The agent drives the same `ocibnkctl` commands as Path 1.

1. Install the binary and create the PoC with your keys (Path 1, steps 1–2).
2. Start the agent **inside the PoC directory**, so it reads `AGENTS.md`:

   ```bash
   cd llmdemo
   claude          # or your agentic CLI of choice
   ```

3. Ask in plain words, for example:

   > Deploy the LLM gateway demo in this PoC end to end and prove it works.
   > Check the host first and tell me what you are about to run before
   > anything destructive.

   > The llm-gateway scenario failed — find out why from the report and the
   > cluster, and fix it if it is safe to.

   > Show me how to open the console and send a request with the demo key.

What to expect: the agent runs `ocibnkctl doctor` and `validate`, asks you to
confirm the destructive `e2e` (the `--confirm-cluster` gate is yours to give),
runs it, and reports the assertions and the Try-it-yourself block. It points
you to where the passwords and the virtual key are rather than printing them,
and it never reads `keys/`.

Non-interactive (CI, a remote box): `claude -p "<the request above>"` from the
PoC directory runs the same workflow and prints the agent's report. What the
agent may run is decided by your Claude Code permission settings; note that
`--allowedTools` only adds to them, so in `auto` or permissive modes it does
not confine the agent. Run it on a machine where that is acceptable.

What it looked like on an arm64 lab box (fresh clone, October 2026): the
agent checked the host, ran `e2e`, found that a stale PoC's docker network
already held the default edge subnet, set `edge_octet` in its own `poc.yaml`
without touching the other PoC, re-ran, and reported 9/9 assertions green in
10½ minutes, including the controller restart for the 2.4.1 route race below.

## Path 3: your own BNK cluster, without ocibnkctl

For a BNK **2.4.1** cluster installed with FLO the usual way (FLO, then a
CNEInstance). This is everything ocibnkctl does for the gateway, as manual
steps. Every field and default below is from FLO `v2.30.0-0.11.4`, the
operator in the 2.4.1 release manifest.

**1. Check the prerequisites.**

| Needs | Why | Check |
|---|---|---|
| BNK 2.4.1 (release manifest `2.4.1`, FLO `v2.30.0-0.11.4`) | the AI gateway, its console image `f5-aigateway-ui:v0.11.0` and the `inference.k8s.f5.com` CRDs ship in 2.4.1 | `kubectl -n f5-operators get deploy -o wide` |
| DSSM deployed, in the default Sentinel mode | the gateway keeps tenants, keys and budgets in DSSM. FLO **skips the gateway silently** (one log warning) when DSSM is disabled or `spec.sessionStore.clustered` is set | `kubectl get pods \| grep f5-dssm-sentinel` |
| `telemetry.loggingSubsystem` on (the default) | only if you want metering: the request records reach ClickHouse through the platform fluentd | the CNEInstance does not set `loggingSubsystem.enabled: false` |
| License **Active** | f5ingress programs nothing without it. A **test** JWT (`jku` `product-tst…`) needs TEEM's current test JWKS: apply [`examples/license-tst-jwt.yaml`](../examples/license-tst-jwt.yaml) (ConfigMap `cwc-jwks-cm` + License with `jwksConfigMapRef`). A production JWT needs neither | `kubectl -n f5-cne-core get license` |

**2. Turn the gateway on in the CNEInstance.** One block in `spec`:

```yaml
spec:
  aiGateway:
    enabled: true          # console, gateway API, DNS resolver
    aiMetrics: true        # optional: ClickHouse + per-request records
    consoleURL: ""         # optional: the URL users open the console at
```

```bash
kubectl patch cneinstance <name> -n <namespace> --type merge \
  -p '{"spec":{"aiGateway":{"enabled":true,"aiMetrics":true}}}'
```

| Field | Default | Effect |
|---|---|---|
| `enabled` | `false` | FLO creates the `AIGateway` CR `f5-aigateway`, which renders the rest |
| `aiMetrics` | `false` | adds ClickHouse 24.8, its schema Job, the HSL receiver in fluentd, and the `aigw-hslpub` publisher TMM logs to. Turning it off later keeps the stored data |
| `consoleURL` | `http://localhost:3000` | becomes the console's `NEXTAUTH_URL`; sign-in redirects go there. Must match `^https?://host…`. Leave it unset to use a port-forward on local port 3000 |

That is the only change FLO needs. With it, FLO installs, in the CNEInstance's
namespace:

- the console: Deployment and NodePort Service `f5-aigateway-console` (port 3000);
- the gateway API: f5-cne-controller restarted with the AI gateway enabled,
  behind Service `f5-cne-controller-api` (port 8443), with its DSSM client
  certificate and extra RBAC;
- `aigw-dns-resolver`, the DNS cache TMM resolves provider hostnames through,
  **only if** Service `kube-system/kube-dns` exists (see step 5);
- Secret `f5-aigateway-admin`, the platform admin (user `admin`);
- with `aiMetrics`: StatefulSet `f5-aigateway-clickhouse`, Secret
  `f5-aigateway-clickhouse-auth`, the receiver pipeline and Service
  `f5-aigateway-hsl-receiver` in fluentd's namespace (`f5-cne-core` by default),
  and the HSL publisher `aigw-hslpub`.

Wait for it:

```bash
kubectl get aigateway f5-aigateway -n <namespace>        # Accepted, Reconciled, Available = True
kubectl get cnecontroller -A -o jsonpath='{..conditions[?(@.type=="AIGatewayDNSResolverReady")].status}'
kubectl get crd | grep inference.k8s.f5.com              # aigatewayprofiles, aiproviders, aimodels, aitenants, aicredentials
```

**3. Create a GatewayClass, if the cluster has none.** FLO does not create
one. Routes made in the console need a GatewayClass handled by this cluster's
controller; its `controllerName` is `f5.com/<cneinstance-namespace>-f5-cne-controller`:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: bnk-gatewayclass
spec:
  controllerName: f5.com/default-f5-cne-controller   # CNEInstance in namespace "default"
```

`kubectl get gatewayclass` shows `ACCEPTED True` once f5-cne-controller has
picked it up. The console doesn't ask for a class: it reuses the one the
existing Gateways in the namespace use, and refuses to create a route when
there are none or they use more than one.

**4. Make a gateway address reachable.** Each route is a Gateway with one
address, served by TMM. Clients reach it the way they reach any BNK Gateway
on this cluster: through the cluster's existing data-plane design (VLANs and
self-IPs, or BGP from TMM). Nothing about this is specific to the AI gateway.
The ocibnkctl demo uses TMM's BGP to an edge router; on your cluster, pick an
address from the range your other Gateways use.

**5. Let TMM reach the providers.** TMM has its own DNS and its own routing
table; the pod network's DNS and egress don't apply to it. Two things are
often missing:

- *DNS for provider hostnames.* FLO creates `aigw-dns-resolver` only from the
  ClusterIP of Service `kube-system/kube-dns`. Clusters that name it otherwise
  (Kubespray: `coredns`) get no resolver, and TMM resets every request to a
  provider given by hostname. The CNEController condition
  `AIGatewayDNSResolverReady=False` (reason `ClusterDNSNotFound`) says so.
  Create it yourself; FLO creates it only once and leaves your copy alone:

  ```bash
  kubectl get f5bigdnscache aigw-dns-resolver -n <namespace>   # NotFound = create it
  kubectl get svc -n kube-system coredns kube-dns              # the cluster DNS ClusterIP
  ```

  ```yaml
  apiVersion: k8s.f5net.com/v1
  kind: F5BigDnsCache
  metadata:
    name: aigw-dns-resolver        # fixed name
    namespace: <namespace>         # the CNEInstance's
  spec:
    cacheType: net-resolver
    netResolver:
      forwardZones:
        - forwardZone: "."
          nameServers:
            - ipAddress: 10.233.0.3   # the cluster DNS ClusterIP
              port: 53
  ```

- *A route out.* If TMM has no default route towards the providers, add one
  through a next hop on one of its VLANs:

  ```yaml
  apiVersion: k8s.f5net.com/v1
  kind: F5SPKStaticRoute
  metadata:
    name: llm-egress-route
    namespace: <namespace>
  spec:
    type: gateway
    destination: 0.0.0.0     # or one provider address with prefixLen: 32
    prefixLen: 0
    gateway: 10.1.10.1       # next hop on the external VLAN
  ```

Neither is needed for a provider inside the cluster, such as the demo's mock.
Symptom when either is missing: `GET /v1/models` works (TMM answers it
itself), chat requests get a connection reset.

**6. Create a tenant, then configure it.** Open the console as the platform
admin, create a tenant, read its admin password, and do the rest in the
tenant's console (see [Tenants and accounts](#tenants-and-accounts)), or
apply the same objects as YAML (see
[Configure with kubectl](#configure-with-kubectl)):

```bash
kubectl port-forward -n <namespace> svc/f5-aigateway-console 3000:3000
kubectl get secret f5-aigateway-admin -n <namespace> -o jsonpath='{.data.password}' | base64 -d; echo
#   http://localhost:3000 → "Sign in as platform admin" → Tenants → Create tenant
kubectl get secret aitenant-<tenant>-admin -n <namespace> -o jsonpath='{.data.password}' | base64 -d; echo
#   sign in to the tenant as admin, then: Providers → Models → Routes → Users → Virtual Keys
```

In the tenant: add a **provider** (vendor, API key, base URL), a **model** on
it (set its **pricing**, or spend shows 0), a **route** with the address
from step 4, a **principal**, and a **virtual key** scoped to the model. Clients then call
`http://<address>/v1/chat/completions` with `Authorization: Bearer <virtual key>`.

To script it instead, the same calls are in
[`llm-setup.sh`](../internal/scenarios/llmgateway/manifests/llm-setup.sh);
run it from a pod in the cluster with the platform admin's password.

**7. Check the route reached TMM.** Changes reach TMM in batches: allow a
few minutes after each change (the UDF lab saw 1–4) before testing, and as
long for a deletion. Beyond that, in 2.4.1 a new route, or a change to its
models or providers, can stop at *"AI profile … is not prepared yet"* and
never be retried; the AIGatewayProfile then reports
`Programmed=False: waiting for Gateway … to reconcile the prepared AI profile`
and requests to the address get no answer.

```bash
kubectl get aigatewayprofiles.inference.k8s.f5.com -n <namespace>
kubectl rollout restart deploy/f5-cne-controller -n <namespace>   # if it is stuck
```

One restart usually clears it. If it does not (seen with several providers
on one route), delete the route in the console, wait until its Gateway and
AIGatewayProfile are gone (one to three minutes, they have finalizers), create
it again, and restart f5-cne-controller once more. The profile's `Programmed`
condition can lag behind: if TMM serves the address, the route works.

**What ocibnkctl adds on top, and you don't need:** the k3s cluster, BNK
itself, the edge router and test client, the mock LLM, and the checks.

## Configure with kubectl

Everything the console creates is a Kubernetes object, except what lives in
DSSM (organizations, teams, principals, budgets, rate limits and virtual
keys). So a tenant, its providers, models and routes can be kept as YAML and
applied with `kubectl` or GitOps. The field names below are those of the
2.4.1 API (`inference.k8s.f5.com/v1alpha1`, Early Access, may change in
2.5.0); `kubectl explain aigatewayprofile.spec --recursive` shows them all.

Two rules shape the design:

- **The listener chooses the tenant.** An AppPolicy attaches one
  AIGatewayProfile to one Gateway listener, and a profile belongs to one
  tenant. Two tenants need two listeners (ports) or two Gateways (VIPs); the
  URL path or hostname can't select a tenant.
- **The model name chooses the provider.** Clients only send a public model
  name. Which provider, upstream model and key serve it is the AIModel's
  business. A profile serves every model of its tenant in its namespace.

How the objects point to each other:

```
AppPolicy ──targetRefs──> Gateway (listener) <──parentRefs── HTTPRoute
    └──extensionRefs──> AIGatewayProfile ──tenantRef──> AITenant
AIModel ──deployments[].providerRef──> AIProvider ──credentialRef──> AICredential ──secretRef──> Secret
```

Put every namespaced object in the **CNEInstance's namespace**. That is
where the console writes (the gateway API files everything under its own
namespace), and the console only sees models and providers there. Every
object except the AITenant carries `spec.tenantRef`, which can't be changed
later.

**Tenant.** Cluster-scoped. The name is lowercase, permanent, and written
into every virtual key. The controller creates Secret
`aitenant-<tenant>-admin` for its local admin.

```yaml
apiVersion: inference.k8s.f5.com/v1alpha1
kind: AITenant
metadata:
  name: acme
spec:
  displayName: ACME Corp
  # auth:
  #   oidc: {issuer: ..., jwksURL: ..., audiences: [...], clientID: ..., clientSecretRef: {...}, identityMapping: {...}}
```

**Provider and its key.** One upstream LLM service; it becomes a pool in
TMM. The key stays in the Secret and is never given to clients.

```bash
kubectl create secret generic acme-openai-key -n <namespace> --from-literal=api-key="$PROVIDER_API_KEY"
```

```yaml
apiVersion: inference.k8s.f5.com/v1alpha1
kind: AICredential
metadata:
  name: acme-openai-key
  namespace: <namespace>
spec:
  tenantRef: {name: acme}
  secretRef: {name: acme-openai-key, key: api-key}
---
apiVersion: inference.k8s.f5.com/v1alpha1
kind: AIProvider
metadata:
  name: acme-vllm
  namespace: <namespace>
spec:
  tenantRef: {name: acme}
  vendor: Generic                         # OpenAI-compatible server (vLLM, LiteLLM, ...)
  generic:
    baseURL: https://vllm.example.com/v1  # include /v1
  credentialRef: {name: acme-openai-key}
  enabled: true
```

`vendor` is one of `OpenAI`, `Anthropic`, `AzureOpenAI`, `AzureFoundry`,
`Bedrock`, `Gemini`, `Generic`, and the settings block is named after it
(`openAI:`, `anthropic:`, `azureOpenAI: {endpoint, apiVersion,
deploymentName}`, `azureFoundry: {endpoint}`, `bedrock: {region}`,
`gemini:`, `generic: {baseURL}`). Public vendors don't need a base URL.

**Model.** The public name clients put in `"model"`, and up to 16
deployments that serve it, each with a provider, an upstream name, a weight
and prices. Several deployments split traffic or fail over between providers.

```yaml
apiVersion: inference.k8s.f5.com/v1alpha1
kind: AIModel
metadata:
  name: acme-qwen
  namespace: <namespace>
spec:
  tenantRef: {name: acme}
  name: Qwen/Qwen3.6-35B-A3B-FP8          # what clients send
  deployments:
    - name: vllm
      providerRef: {name: acme-vllm}
      upstream: Qwen/Qwen3.6-35B-A3B-FP8  # what the provider is sent
      dialects: [openai]                  # Generic: openai and/or anthropic
      weight: 1
      state: Active                       # or Paused
      pricing:                            # micro-USD per million tokens
        inputPerMtok: 10000000            # $10 / M tokens
        outputPerMtok: 50000000
```

Without `pricing`, catalogued models fall back to the catalog price and
others show a spend of 0. For a `Generic` provider, list the dialects the
server speaks; other vendors default to their native one.

**Route.** Four objects: the Gateway (VIP and listener), an HTTPRoute (which
paths are AI traffic, no backend), the AIGatewayProfile (the AI behaviour)
and the AppPolicy that attaches the profile to the listener. The console's
New route creates the same four.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: acme-llm
  namespace: <namespace>
spec:
  gatewayClassName: bnk-gatewayclass       # Path 3, step 3
  addresses:
    - {type: IPAddress, value: 192.0.2.10}
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        kinds: [{kind: HTTPRoute}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: acme-llm
  namespace: <namespace>
spec:
  parentRefs:
    - {name: acme-llm, sectionName: http}
  rules:
    - matches:
        - path: {type: PathPrefix, value: /v1}  # the console uses "/"
      timeouts: {request: 300s, backendRequest: 300s}
      # no backendRefs: the profile picks the destination
---
apiVersion: inference.k8s.f5.com/v1alpha1
kind: AIGatewayProfile
metadata:
  name: acme-llm
  namespace: <namespace>
spec:
  tenantRef: {name: acme}
  auth:
    virtualKeys: {}
---
apiVersion: gateway.k8s.f5.com/v1alpha1
kind: AppPolicy
metadata:
  name: acme-llm
  namespace: <namespace>
spec:
  targetRefs:
    - {group: gateway.networking.k8s.io, kind: Gateway, name: acme-llm, sectionName: http}
  extensionRefs:
    - {group: inference.k8s.f5.com, kind: AIGatewayProfile, name: acme-llm}
```

Matching `/v1` instead of the console's `/` keeps other paths on the VIP out
of the gateway. The console shows routes created with kubectl read-only.

**Gateway features.** Optional blocks of the AIGatewayProfile; a block that
is present switches the feature on (console: **Policies**).

```yaml
spec:
  auth:
    virtualKeys: {}
    # jwt: {audiences: [api://my-app]}     # JWTs from the tenant's OIDC issuer
  failover:                                # retry another deployment
    on429: true
    on5xx: true
    maxAttempts: 2
    cooldown: 30s
    honorRetryAfter: true
  # promptCache: {}
  # modelRouting:
  #   semanticRouter: {serviceRef: {name: semantic-router, port: 8080}, timeout: 2s}
  # providers: [{providerRef: {name: acme-vllm}}]   # limit providers; unset = all of the tenant's
```

**Organizations, teams, principals, budgets, rate limits, virtual keys.** Not
Kubernetes objects: use the console or the gateway API (base
`https://f5-cne-controller-api.<namespace>.svc:8443/api/v1/tenants/<tenant>`,
paths `/governance/orgs`, `/governance/teams`, `/governance/principals`,
`/governance/budgets`, `/virtual-keys`). A request must fit the budget and
rate limit of its principal, team and organization;
[`llm-setup.sh`](../internal/scenarios/llmgateway/manifests/llm-setup.sh)
shows the calls.

**Test.** `GET /v1/models` is answered by TMM itself; only a chat request
proves the path to the provider:

```bash
VK=f5vk_...; BASE=http://192.0.2.10/v1
curl -s $BASE/models -H "Authorization: Bearer $VK"
curl -s -m 120 $BASE/chat/completions -H "Authorization: Bearer $VK" \
  -H 'Content-Type: application/json' \
  -d '{"model":"Qwen/Qwen3.6-35B-A3B-FP8","messages":[{"role":"user","content":"Say OK"}]}'
```

The ocibnkctl demo itself is configured through the API, not this YAML.
These manifests were checked against the 2.4.1 API types and the UDF 2.4.1
lab manual, not run by the `llm-gateway` scenario.

## Open the console

Sign-in is **per tenant**. The demo tenant is `demo`, user `admin`; its
password is in a Secret that FLO creates for each tenant. The profile puts
three scripts in the PoC for this:

```bash
./console.sh             # forward the console to localhost:3000 (Ctrl-C stops it)
./demo-password.sh       # tenant demo's admin password; ./demo-password.sh acme for another tenant
./admin-password.sh      # the platform admin's password
```

By hand, they run:

```bash
export KUBECONFIG=$PWD/artifacts/kubeconfig
kubectl get secret aitenant-demo-admin -o jsonpath='{.data.password}' | base64 -d; echo
kubectl get secret f5-aigateway-admin -o jsonpath='{.data.password}' | base64 -d; echo
kubectl port-forward svc/f5-aigateway-console 3000:3000
```

Open <http://localhost:3000>, enter tenant `demo` (the page proposes `lab`:
replace it), choose **Sign in as tenant admin**, then `admin` and the
password. Use **localhost:3000** exactly: the console's sign-in redirects go
there by default (FLO's `consoleURL`).

From a laptop, with the cluster on another machine:

```bash
ssh -L 3000:127.0.0.1:3000 <cluster-host>
# on the cluster host, in the PoC directory:
KUBECONFIG=artifacts/kubeconfig kubectl port-forward svc/f5-aigateway-console 3000:3000
```

What to show: the **dashboard** (requests, errors, tokens, cost, p50/p95
latency), **Spend** by model, **Providers** (the mock), **Models**, **Routes**
(the `demo-llm` Gateway) and **Keys**. The console is branded "Polaris AI" in
2.4.1; it is F5's shipped `f5-aigateway-ui` image.

## Tenants and accounts

A **tenant** is one customer of the gateway: its own providers, models,
routes, keys and spend, and its own console sign-in. Inside a tenant,
**principals** (users, agents, services) belong to **teams**, teams to
**organizations**, and **virtual keys** are issued to principals.

| What | In the console | Through the API | Who may |
|---|---|---|---|
| Create a tenant | **Tenants → Create tenant** | `POST /api/v1/tenants` `{name, displayName}` | platform admin only |
| Its first sign-in | — (the console never shows the password) | — | `./demo-password.sh <tenant>`, or read Secret `aitenant-<tenant>-admin` with `kubectl` |
| Single sign-on (OIDC) for a tenant | shown on **Tenant settings** ("Local admin only" or the issuer); no form to set it | `PUT /api/v1/tenants/{t}/auth` | platform admin only |
| Organizations, teams | **Organizations**, **Teams** (budget, rate limit, model access per team) | `POST …/governance/orgs`, `…/teams` | tenant admin |
| Users, agents, services | **Users** (name, kind, team, email, role, model scope) | `POST …/governance/principals` | team admin and up |
| Virtual keys | **Virtual Keys → New key** (the token is shown once) | `POST …/virtual-keys` | team admin and up |

**Signing in.** There are two kinds of console account, and only two:

- The **platform admin** (user `admin`, Secret `f5-aigateway-admin`,
  `./admin-password.sh`): the "Sign in as platform admin" button. Creates and deletes tenants and sets
  their SSO. Its API token also reaches every tenant's objects (this is how
  the setup script works), but its password is refused on a tenant's own
  sign-in page (401).
- Each tenant's **local admin** (user `admin`, Secret
  `aitenant-<tenant>-admin`, `./demo-password.sh <tenant>`, minted when the tenant is created and never
  regenerated): enter the tenant name, then "Sign in as tenant admin".
  The sign-in page proposes tenant `lab`, which doesn't exist here:
  replace it (with `demo` for the scenario's tenant).

Every other person signs in through the tenant's **SSO**: once OIDC is set
for the tenant, a sign-in is joined to the principal whose **email** matches
the token's verified email. A principal without SSO has no console sign-in;
it uses the gateway with a virtual key only. The local admin is the only
password-based account per tenant.

So for a customer demo: create the tenant in the console as platform admin,
read its admin password once with `kubectl`, and do everything after that in
the console as the tenant admin.

**How ocibnkctl does it.** The `llm-gateway` scenario's `llm-setup.sh` makes
the same API calls from an in-cluster Job, signed in as the platform admin:
tenant `demo`, a provider key, a model, principal `demo-app` in the default
team, route `demo-llm`, and a virtual key scoped to the model. It then points
to `aitenant-demo-admin` for the console sign-in. Team ids are generated by
the gateway (`t-<hex>`), so teams are created by name; models and routes of
every tenant share one Kubernetes namespace, so the script prefixes their
names with the tenant.

## Send your own requests

`init --profile llm-gateway` puts `ask.sh` in the PoC directory. It sends one
prompt as tenant `demo`'s principal `demo-app`, through TMM, from the edge
network outside the cluster, and prints the reply (needs `docker` and `jq`):

```bash
./ask.sh "what is BNK?"            # the reply, then "-- gpt-4o-mini, 62 tokens"
./ask.sh "hi" gpt-5                # HTTP 403: Model not allowed for this virtual key
KEY=sk-... ./ask.sh "hi"           # another principal's virtual key
RAW=1 ./ask.sh "hi"                # the full JSON response
```

The mock answers with canned text; with a [real provider](#use-a-real-provider)
behind the model, the same script gets real answers.

What it does, by hand: the gateway address `203.0.113.140` is reachable from
the edge network, where the scenario's client runs. The demo's virtual key is
in `artifacts/llm-gateway/virtual-key` (mode 0600).

```bash
edge() {   # curl from the edge, outside the cluster
  docker run --rm --network container:bnk-edge-frr-llmdemo curlimages/curl:8.10.1 -s "$@"
}
KEY=$(cat artifacts/llm-gateway/virtual-key)

edge http://203.0.113.140/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'      # 200

edge http://203.0.113.140/v1/chat/completions -H "Authorization: Bearer nope" \
  -H 'Content-Type: application/json' -d '{"model":"gpt-4o-mini","messages":[]}'  # 401

edge http://203.0.113.140/v1/chat/completions -H "Authorization: Bearer $KEY" \
  -H 'Content-Type: application/json' -d '{"model":"gpt-5","messages":[]}'        # 403
```

Then refresh the console: each request appears in usage and spend within a
few seconds. To query the raw records:

```bash
kubectl exec f5-aigateway-clickhouse-0 -- clickhouse-client -q "
  SELECT \`http.response.status_code\` AS status, count() AS requests,
         sum(\`gen_ai.f5.usage.total_tokens\`) AS tokens
  FROM otel.aigw_requests WHERE tenant = 'demo' GROUP BY status ORDER BY status"
```

The gateway writes record version 2: its values are in the OpenTelemetry
`gen_ai.*` and `http.response.status_code` columns.

## Use a real provider

The mock needs no key. To put a real provider behind the same virtual keys,
use the console, the way a customer would:

1. **Providers → Add**: vendor (OpenAI, Anthropic, Azure OpenAI, Azure
   Foundry, Bedrock, Gemini or Generic), its API key and, for OpenAI-compatible
   endpoints, the base URL including `/v1`.
2. **Models**: add a deployment of the model on that provider, or a new
   model. Clients keep sending the same `"model"` name.
3. **Keys**: a key's model scope decides which models it may use.

TMM, not the pod network, makes the call: it resolves the provider's hostname
through `aigw-dns-resolver` and routes it with its own routing table, so a
public provider may need the resolver and a static route from
[Path 3, step 5](#path-3-your-own-bnk-cluster-without-ocibnkctl). Set the
model's pricing, or spend shows 0. The provider
key is stored as a Kubernetes Secret; clients only ever hold virtual keys.
Mind your provider's costs and terms; this path was not part of the verified
run, which used the mock only.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `TMM has the demo-llm virtual server` fails, or the AIGatewayProfile says *"waiting for Gateway … to reconcile the prepared AI profile"* | f5ingress 2.4.1: a new route's Gateway apply fails with *"AI profile … is not prepared yet"* and is not retried | The scenario restarts `f5-cne-controller` once by itself. By hand: `kubectl rollout restart deploy/f5-cne-controller` |
| `edge FRR learned …/32` fails | No BGP session, or the virtual server is missing (row above) | `ocibnkctl scenario run bgp-peer-frr`, then re-run `llm-gateway` |
| Console sign-in refused | Wrong tenant on the sign-in page, or the wrong password for it | The page proposes tenant `lab`: replace it with yours (`demo`). Use `aitenant-<tenant>-admin`. `f5-aigateway-admin` is the API's platform admin and is not accepted on the tenant page |
| Console redirects to a dead page | Not on `http://localhost:3000` | Port-forward to local port 3000, or set `bnk.ai_gateway.console_url` in `poc.yaml` and `ocibnkctl deploy cne` |
| `llm-gateway needs bnk.ai_gateway.enabled` | PoC created without the profile | Add the `ai_gateway` block to `poc.yaml`, `ocibnkctl deploy cne`, then the scenario |
| A setup step says *"the name is taken by another tenant"* | Models and routes of all tenants share namespace `default` | Use a different tenant; the script prefixes object names with it |
| ClickHouse assertion missing | `ai_metrics: false` | Expected: metering is off. Set it true and `ocibnkctl deploy cne` |
| `cluster up` fails with *"Pool overlaps with other one on this address space"* | Another (maybe stale) PoC's docker network holds the edge subnet `192.168.99.0/24` | Set `cluster.edge_octet` to a free octet in `poc.yaml` (e.g. `98`), or destroy the old PoC. `docker network ls` shows them |
| 404 | URL outside the route, e.g. no `/v1` | Clients use `http://<address>/v1` as base URL |
| 404 *"No provider serves the requested model"* | `"model"` isn't the model's public name | Use the name shown under **Models** |
| 401 | Wrong or revoked key, or a key of another tenant than the route's | Issue a key in the route's tenant |
| 429 | Budget or rate limit of the principal, team or organization reached | Check the caps under **Teams** and **Users** |
| `/v1/models` works, chat gets a connection reset | TMM can't resolve or reach the provider | [Path 3, step 5](#path-3-your-own-bnk-cluster-without-ocibnkctl): `aigw-dns-resolver` and a route out |
| 405 from the provider | `Generic` base URL missing `/v1` | Edit the provider |
| All models stop working after a change | One broken deployment stops the whole profile | Check every model shows its deployments Active |
| Pods stuck in `ImagePullBackOff`, events say `x509: certificate signed by unknown authority` | A TLS-inspecting proxy (Netskope, Zscaler) re-signs registry traffic and the k3s nodes don't trust its CA | Export the CA (macOS, Netskope: `security find-certificate -a -c goskope -p > keys/netskope-ca.pem`), list it under `cluster.extra_ca_certs` in `poc.yaml`, then `destroy` and rerun `e2e` |
| Host under 10 cores | Resource floor | `init` writes `host_profile: small` on tight hosts and `e2e` adds `deploy shrink` automatically. ClickHouse adds pods: with `ai_metrics: false` the gateway, console and keys still work |

Details on anything else: `kubectl -n llm-demo logs job/llm-setup`,
`kubectl get aigatewayprofiles.inference.k8s.f5.com -o wide`, and the run
report under `reports/`.

## Limits in 2.4.1

From the UDF 2.4.1 lab manual and this demo:

- **One tenant per listener.** No path- or hostname-based tenancy on a shared VIP and port.
- **One namespace.** Console objects of every tenant go to the CNEInstance's namespace; keep YAML there too.
- **One broken model deployment stops the whole profile.** Check models after each change.
- **Not available yet:** guardrails, DLP and PII masking; virtual key expiry and per-key spend caps; OpenTelemetry export; the OpenAI Responses API. Most are planned for 2.5.0.
- **Early Access API.** `inference.k8s.f5.com/v1alpha1` may change in 2.5.0.
- **Branding.** The console is titled "Polaris AI" and can't be renamed by configuration.
- **Stuck routes.** See [Path 3, step 7](#path-3-your-own-bnk-cluster-without-ocibnkctl).

## Tear down

```bash
ocibnkctl destroy --yolo --confirm-cluster llmdemo
```

Removes the cluster containers and networks and restores your
`~/.kube/config`; the PoC directory, its journal and reports stay.

## References

Public:

- BNK 2.4 documentation — [release notes 2.4.0](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.4/releases/release-notes-bnk-2.4.0.html),
  [install a CNE instance with FLO](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.4/install/bnk-host-self-managed-flo/install/install-a-cne-instance-using-flo.html),
  [install with Helm](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.4/install/bnk-host-cnfs-self-managed-helm/install/install-a-cne-instance-using-helm.html),
  [CRDs](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.4/custom-resource-definitions/),
  [how-tos](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.4/how-tos/).
  As of 2026-10-18 they cover 2.4.0; 2.4.1 and the AI gateway are not documented there yet.
- [ocibnkctl](https://github.com/mwiget/ocibnkctl) — this tool; `AGENTS.md`
  in every PoC is the agent's manual.
- [llm-d-inference-sim](https://github.com/llm-d/llm-d-inference-sim) — the mock LLM.
- [OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/) — the `gen_ai.*` record fields.

F5 internal (F5 network or SSO; not reachable for customers):

- PDR *BNK as LLM Gateway* — `docs.f5net.com`, space PDBIPNEXTSP, page 1262160949.
- Jira initiative BIPNXTSPPM-2960 *BNK as LLM Gateway*; FLO epic MBIPAAP-53135.
- Source: `gitswarm.f5net.com` `f5ingress/f5-lifecycle-operator`
  (`internal/controller/aigateway`), `f5ingress/f5ingress` (`internal/api`),
  `f5ingress/ui` (the console).

What was verified, and how: BNK 2.4.1 (release manifest
`f5-bigip-k8s-manifest:2.4.1`, FLO `v2.30.0-0.11.4`,
`f5-aigateway-ui:v0.11.0`, `clickhouse-server:24.8`) on an x86_64 lab host
and, from a fresh clone driven by Claude Code through `AGENTS.md`, on an arm64
lab host, October 2026, with the mock LLM. Every behaviour above is asserted by the
`llm-gateway` scenario or was observed on that cluster, except where a
section says otherwise.
