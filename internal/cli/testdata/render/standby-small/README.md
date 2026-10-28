# BNK 2.4.1 manifests — PoC golden

Rendered by `ocibnkctl render` (dev). Every file here is what `ocibnkctl deploy …`
and `ocibnkctl scenario run …` would apply for this PoC; apply them in the order below
to install BNK by hand, on this cluster or any other.

| Setting | Value |
|---|---|
| CNE release manifest | `2.4.1` |
| FLO chart | _unresolved — see below_ |
| f5-cert-gen chart | _unresolved — see below_ |
| TMM dataplane mode | `standby` |
| TMM workers (`app=f5-tmm`) | 1 |
| Host profile | `small` |

Unresolved chart versions: run `ocibnkctl manifest probe` (needs only the FAR key), or
`helm pull oci://repo.f5.com/release/f5-bigip-k8s-manifest --version 2.4.1` and read the BOM.

All commands assume `kubectl`/`helm` already point at the target cluster, and that you
have logged in to the F5 registry: see [registry login](#registry-login). On macOS use
`shasum -a 256 -c` for `sha256sum -c` and `sed -i ''` for `sed -i`.

## 00-prereqs

```sh
kubectl apply -f 00-prereqs/01-namespaces.yaml
```

**FAR pull secret** `far-secret` in `f5-operators`, `f5-cne-core`, `default`. The FAR tarball holds a base64 GCP
service-account key (`cne_pull_64.json`); the registry user is `_json_key_base64`:

```sh
tar -xzf keys/f5-far-auth-key.tgz   # → cne_pull_64.json
for ns in f5-operators f5-cne-core default; do
  kubectl -n $ns create secret docker-registry far-secret --docker-server=repo.f5.com \
    --docker-username=_json_key_base64 --docker-password="$(tr -d '\n' < cne_pull_64.json)" \
    --dry-run=client -o yaml | kubectl apply -f -
done
```

**cert-manager** v1.16.2:

```sh
helm upgrade --install cert-manager cert-manager --repo https://charts.jetstack.io \
  --namespace cert-manager --create-namespace --version v1.16.2 --set installCRDs=true
kubectl -n cert-manager wait --for=condition=Available deployment/cert-manager-webhook --timeout=5m
```

## 10-flo

```sh
kubectl apply -f 10-flo/01-bnk-ca-issuer.yaml
kubectl -n cert-manager wait --for=condition=Ready certificate/bnk-ca --timeout=3m

helm upgrade --install flo oci://repo.f5.com/charts/f5-lifecycle-operator --version ${FLO_CHART_VERSION} \
  --namespace f5-operators --create-namespace -f 10-flo/02-flo-values.yaml
kubectl -n f5-operators wait --for=condition=Available deployment \
  -l app.kubernetes.io/name=f5-lifecycle-operator --timeout=5m
```

**CWC API certs.** `gen_cert.sh` from the f5-cert-gen chart needs `make`, `openssl` and
`python3` (or run it inside `alpine/k8s:1.31.5`):

```sh
helm pull oci://repo.f5.com/utils/f5-cert-gen --version ${CERT_GEN_CHART_VERSION} --untar
sh cert-gen/gen_cert.sh -s=api-server -a=f5-spk-cwc.f5-cne-core.svc.cluster.local -n=1
kubectl -n f5-cne-core apply -f cwc-license-certs.yaml -f cwc-license-client-certs.yaml
```

## 15-shrink (optional)

Only for hosts below the resource floor (`ocibnkctl doctor`). FLO reasserts every workload
spec, so requests are capped at admission by Kyverno, not in chart values. Must be admitting
**before** 20-cne creates the TMM/DSSM pods.

```sh
helm upgrade --install kyverno kyverno --repo https://kyverno.github.io/kyverno/ --namespace kyverno --create-namespace \
  --version 3.8.1 --set backgroundController.enabled=false --set cleanupController.enabled=false --set reportsController.enabled=false --set admissionController.replicas=1
kubectl -n kyverno wait --for=condition=Available deployment/kyverno-admission-controller --timeout=5m
kubectl apply -f 15-shrink/01-kyverno-shrink-requests.yaml
```

This PoC uses `host_profile: small`, which assumes this step.

## 20-cne

**CNEInstance** — FLO installs the BNK CRDs (License among them) in response to it:

```sh
kubectl apply -f 20-cne/01-cne-instance.yaml
kubectl wait --for=create crd/licenses.k8s.f5net.com --timeout=8m   # first pull of the manifest can be slow
kubectl wait --for=condition=Established crd/licenses.k8s.f5net.com --timeout=3m
```

**License** — skip if `kubectl -n f5-cne-core get license.k8s.f5net.com f5-cne-cluster-license` already reports Active: re-applying
makes CWC re-register, which fails on an already-registered asset. Retry the apply for ~100s if it
fails with "status unknown for quota" (CWC is still creating its ResourceQuota).

For a **test** JWT (`jku` on `product-tst…`): apply `20-cne/02-license-jwks-configmap.yaml` first and add to the License spec
`jwksConfigMapRef: {name: cwc-jwks-cm, key: key}` — CWC's built-in test certs expired. Production JWTs
need neither.

```sh
BNK_JWT="$(tr -d '\n' < keys/.jwt)" sh lib/render.sh 20-cne/02-license.yaml.tmpl | kubectl apply -f -
kubectl -n f5-cne-core wait --for=jsonpath='{.status.state}'=Active license.k8s.f5net.com/f5-cne-cluster-license --timeout=20m
```

**GatewayClass** — wait for Accepted before creating any Gateway, or the Gateway can stick at
"Waiting for controller":

```sh
kubectl apply -f 20-cne/03-gatewayclass.yaml
kubectl wait --for=condition=Accepted gatewayclass/bnk-gatewayclass --timeout=5m
```

**Infra** — the IPAM pool `bnk-dynamic-vips` (203.0.113.110–203.0.113.119) for address-less Gateways. Retry if the
`f5-single-infra-quota` is not populated yet:

```sh
kubectl apply -f 20-cne/04-infra.yaml
kubectl -n default wait --for=condition=Programmed infras.gateway.k8s.f5.com/infra --timeout=3m
```

Done when `daemonset/f5-tmm` is ready on every `app=f5-tmm` node and every workload in
`f5-cne-core` is Available.

## scenarios

One directory per scenario, in dependency order. The numbered files are the manifests; each
scenario's README says what it checks. Most assume the `bnk-gatewayclass` GatewayClass and VIPs in
203.0.113.0/24 advertised to a BGP peer — adapt them for another cluster. Apply with
`kubectl apply -f scenarios/<name>/` (files apply in name order); remove with
`kubectl delete -f scenarios/<name>/`.

Data-plane checks: `ocibnkctl scenario run` curls the VIPs from inside the external FRR container
(`bnk-edge-frr-golden`), which learns them from TMM over BGP. On another cluster, curl from any
host that learns those routes from your BGP peer.

| Scenario | Rating | Depends on | Title |
|---|---|---|---|
| [bgp-peer-frr](scenarios/bgp-peer-frr/README.md) | green | — | BGP peering: TMM OcNOS ⇄ external FRR (ToR) |
| [ai-inference-epp](scenarios/ai-inference-epp/README.md) | green | bgp-peer-frr | AI Inference Routing with the F5 Endpoint Picker — F5EPP + GAIE InferencePool, mock vLLM |
| [ai-semantic-cache](scenarios/ai-semantic-cache/README.md) | green | bgp-peer-frr | Semantic AI Model Caching (how-to #7) — k8s.f5.com/ai semantic_cache + sse-enabled |
| [ai-token-counting](scenarios/ai-token-counting/README.md) | green | bgp-peer-frr | Token Counting and Enforcement (how-to #6) — k8s.f5.com/ai-token-counting Gateway annotation |
| [ai-token-counting-dssm](scenarios/ai-token-counting-dssm/README.md) | green | bgp-peer-frr | AI Token Counting via custom iRule + DSSM/Redis — 4 LBs, streaming + non-streaming, persisted for tmm-stat-exporter |
| [bgp-anycast](scenarios/bgp-anycast/README.md) | green | bgp-peer-frr | BGP anycast all-active data plane (how-to #3) — every TMM advertises over the shared edge FRR |
| [cluster-wide-watch](scenarios/cluster-wide-watch/README.md) | green | — | Components needing cluster-wide access (how-to #2) — single controller reconciling a new namespace |
| [core-file-collection](scenarios/core-file-collection/README.md) | green | — | Core file collection (how-to #4) — CNEInstance.spec.coreCollection.enabled + CoreMond |
| [cwc-admin-access](scenarios/cwc-admin-access/README.md) | green | — | Restrict access to sensitive data (how-to #1) — bearer token + mTLS to CWC admin API |
| [external-resource-pool](scenarios/external-resource-pool/README.md) | green | bgp-peer-frr | Load Balance Traffic to External Resources (how-to #10) — Pool CR + HTTPRoute backendRef |
| [fic-dynamic-ip](scenarios/fic-dynamic-ip/README.md) | green | bgp-peer-frr | Dynamic IP address allocation (FIC for Gateway API) — address-less Gateway + GatewaySettings/Infra IPAM |
| [grpc-loadbalance](scenarios/grpc-loadbalance/README.md) | green | bgp-peer-frr | gRPC load balancing — L4Route (TCP) data plane; GRPCRoute control plane (kong/grpcbin) |
| [http-routing-e2e](scenarios/http-routing-e2e/README.md) | green | bgp-peer-frr | HTTP traffic steering with Gateway API HTTPRoute (how-to #8) — full data plane via NAD |
| [llm-gateway](scenarios/llm-gateway/README.md) | green | bgp-peer-frr | LLM gateway (BNK 2.4.1 AI gateway) — virtual keys, model scope, metering, console |
| [proxy-protocol-l4](scenarios/proxy-protocol-l4/README.md) | green | bgp-peer-frr | Proxy Protocol iRule on an L4 route (how-to #9) — F5BigCneIrule + L4Route + NetPolicy |
| [routing-template-multitenant](scenarios/routing-template-multitenant/README.md) | amber | — | Expose VLAN/Self-IPs in a RoutingTemplate (.vlans) — two tagged tenants on one interface |
| [routing-config-tenant](scenarios/routing-config-tenant/README.md) | amber | routing-template-multitenant | Per-tenant routing (RoutingConfig) — .tenant + per-tenant route domain in the template context |
| [tcp-l4-loadbalance](scenarios/tcp-l4-loadbalance/README.md) | green | bgp-peer-frr | TCP load balancer via L4Route — weighted backends (70/30) |
| [udp-l4-loadbalance](scenarios/udp-l4-loadbalance/README.md) | green | bgp-peer-frr | UDP load balancer via L4Route — alpine/socat echo backend |

## Registry login

`helm` pulls from `oci://repo.f5.com` need a login with the FAR credential:

```sh
tr -d '\n' < cne_pull_64.json | helm registry login repo.f5.com --username _json_key_base64 --password-stdin
```

`lib/render.sh` fills the `${NAME}` placeholders in the `.tmpl` files from environment
variables (POSIX sh + awk, also on macOS); see its header.
