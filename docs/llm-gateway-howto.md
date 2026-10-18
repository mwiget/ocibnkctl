# How to: an LLM gateway demo on BNK 2.4.1 with ocibnkctl

A self-contained demo of the **AI/LLM gateway in BIG-IP Next for Kubernetes
(BNK) 2.4.1**, on one machine with Docker. No GPU, no LLM API key, no access
to anything beyond F5's normal image registry. Two paths, same result:

- **Manual:** five commands, about 30 minutes the first time.
- **Agent-assisted:** a CLI coding agent (Claude Code, or any agent that reads
  `AGENTS.md`) runs and explains it for you.

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
- [Open the console](#open-the-console)
- [Send your own requests](#send-your-own-requests)
- [Use a real provider](#use-a-real-provider)
- [Troubleshooting](#troubleshooting)
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

## Open the console

Sign-in is **per tenant**. The demo tenant is `demo`, user `admin`; its
password is in a Secret that FLO creates for each tenant.

```bash
export KUBECONFIG=$PWD/artifacts/kubeconfig
kubectl get secret aitenant-demo-admin -o jsonpath='{.data.password}' | base64 -d; echo
kubectl port-forward svc/f5-aigateway-console 3000:3000
```

Open <http://localhost:3000>, enter tenant `demo`, then `admin` and the
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

## Send your own requests

The gateway address `203.0.113.140` is reachable from the edge network, where
the scenario's client runs. The demo's virtual key is in
`artifacts/llm-gateway/virtual-key` (mode 0600).

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

TMM reaches the provider from inside the cluster through the CNI's egress and
resolves its hostname through `aigw-dns-resolver`, created by FLO. The provider
key is stored as a Kubernetes Secret; clients only ever hold virtual keys.
Mind your provider's costs and terms; this path was not part of the verified
run, which used the mock only.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `TMM has the demo-llm virtual server` fails, or the AIGatewayProfile says *"waiting for Gateway … to reconcile the prepared AI profile"* | f5ingress 2.4.1: a new route's Gateway apply fails with *"AI profile … is not prepared yet"* and is not retried | The scenario restarts `f5-cne-controller` once by itself. By hand: `kubectl rollout restart deploy/f5-cne-controller` |
| `edge FRR learned …/32` fails | No BGP session, or the virtual server is missing (row above) | `ocibnkctl scenario run bgp-peer-frr`, then re-run `llm-gateway` |
| Console sign-in refused | Wrong password for the tenant | Use `aitenant-<tenant>-admin`. `f5-aigateway-admin` is the API's platform admin and is not accepted on the tenant page |
| Console redirects to a dead page | Not on `http://localhost:3000` | Port-forward to local port 3000, or set `bnk.ai_gateway.console_url` in `poc.yaml` and `ocibnkctl deploy cne` |
| `llm-gateway needs bnk.ai_gateway.enabled` | PoC created without the profile | Add the `ai_gateway` block to `poc.yaml`, `ocibnkctl deploy cne`, then the scenario |
| A setup step says *"the name is taken by another tenant"* | Models and routes of all tenants share namespace `default` | Use a different tenant; the script prefixes object names with it |
| ClickHouse assertion missing | `ai_metrics: false` | Expected: metering is off. Set it true and `ocibnkctl deploy cne` |
| `cluster up` fails with *"Pool overlaps with other one on this address space"* | Another (maybe stale) PoC's docker network holds the edge subnet `192.168.99.0/24` | Set `cluster.edge_octet` to a free octet in `poc.yaml` (e.g. `98`), or destroy the old PoC. `docker network ls` shows them |
| Host under 10 cores | Resource floor | `init` writes `host_profile: small` on tight hosts and `e2e` adds `deploy shrink` automatically. ClickHouse adds pods: with `ai_metrics: false` the gateway, console and keys still work |

Details on anything else: `kubectl -n llm-demo logs job/llm-setup`,
`kubectl get aigatewayprofiles.inference.k8s.f5.com -o wide`, and the run
report under `reports/`.

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
