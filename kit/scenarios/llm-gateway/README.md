# `llm-gateway` — BNK 2.4.1 AI gateway: virtual keys, model scope, metering

Rating: 🟢 &nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgp-peer-frr)

Needs BNK 2.4.1+ with the AI gateway enabled in the CNEInstance
(`spec.aiGateway.enabled`; with ocibnkctl, `bnk.ai_gateway.enabled` in
poc.yaml or `ocibnkctl init <name> --profile llm-gateway`) and the
anycast-bgp data plane. The full walk-through is
[docs/llm-gateway-howto.md](../../../docs/llm-gateway-howto.md).

`apply.sh` deploys a mock LLM (llm-d-inference-sim: OpenAI-compatible, no GPU,
no API key), then runs [`manifests/llm-setup.sh`](manifests/llm-setup.sh) as a
Job against the AI gateway API: tenant `demo`, a provider pointing at the
mock, model `gpt-4o-mini`, principal `demo-app`, route `demo-llm` on
203.0.113.140:80 (Gateway + HTTPRoute + AppPolicy + AIGatewayProfile), and a
virtual key scoped to the model. The key is saved to `KEY_FILE` (under
ocibnkctl: `<PoC>/artifacts/llm-gateway/virtual-key`, where the profile's
`ask.sh` reads it); the platform admin password and provider key only pass
through 0600 files and pipes, never a command line or the output.

`verify.sh`, from a client outside the cluster:

- TMM has the route's virtual server — restarting `f5-cne-controller` once
  if the f5ingress 2.4.1 "AI profile ... is not prepared yet" race left it out
- the edge learned the VIP over BGP
- the virtual key gets a 200 with a usage block; a wrong key and no key get
  401; a model outside the key's scope gets 403
- with AI metrics on, ClickHouse holds records for the 200, 401 and 403

## How to run

```bash
ocibnkctl scenario run llm-gateway --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh), edge client:
[kit/lib/edge.sh](../../lib/edge.sh)):

```sh
sh kit/scenarios/llm-gateway/apply.sh
EDGE_DRIVER=none sh kit/scenarios/llm-gateway/verify.sh    # control plane only; request checks skipped
sh kit/scenarios/llm-gateway/cleanup.sh
```

Variables and their defaults are in [`scenario.env`](scenario.env).

Cleanup deletes the `llm-demo` namespace. The tenant and its objects stay —
the AI gateway API owns them; delete them in the console or with the API.
