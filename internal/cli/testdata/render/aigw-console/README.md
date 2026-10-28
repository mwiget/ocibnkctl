# BNK 2.4.1 manifests — PoC golden

Rendered by `ocibnkctl render` (dev). Every file here is what `ocibnkctl deploy …`
and `ocibnkctl scenario run …` would apply for this PoC; apply them in the order below
to install BNK by hand, on this cluster or any other.

| Setting | Value |
|---|---|
| CNE release manifest | `2.4.1` |
| FLO chart | _unresolved — see below_ |
| f5-cert-gen chart | _unresolved — see below_ |
| TMM dataplane mode | `anycast-bgp` |
| TMM workers (`app=f5-tmm`) | 1 |
| Host profile | `standard` |

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

## 20-cne

**Multus** (thick plugin) — download, check, apply, then the two fixes `deploy cne` patches in
(rename-based shim install against the restart deadlock; 500Mi limit against OOMKills):

```sh
curl -fsSLo multus.yaml https://raw.githubusercontent.com/k8snetworkplumbingwg/multus-cni/v4.1.4/deployments/multus-daemonset-thick.yml
echo '33fef64fbb67ef5d68183bad5b2aec4163dad0ebb0b63abe25343155d0d8b4be  multus.yaml' | sha256sum -c -
kubectl apply -f multus.yaml
kubectl -n kube-system patch daemonset/kube-multus-ds --type=json -p '[{"op":"replace","path":"/spec/template/spec/initContainers/0/command","value":["/usr/bin/sh","-c","cp -f /usr/src/multus-cni/bin/multus-shim /host/opt/cni/bin/multus-shim.tmp && mv -f /host/opt/cni/bin/multus-shim.tmp /host/opt/cni/bin/multus-shim"]}]'
kubectl -n kube-system patch daemonset/kube-multus-ds --type=json -p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/memory","value":"500Mi"},{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/cpu","value":"100m"},{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/memory","value":"200Mi"}]'
kubectl -n kube-system rollout status daemonset/kube-multus-ds --timeout=3m
```

With 15-shrink applied, use its requests (cpu 25m, memory 128Mi) in the second patch.

Every node also needs the `bridge` CNI plugin in `/opt/cni/bin` (k3s ships it as `/bin/bridge`;
`deploy cne` copies it there on each node).

**Whereabouts** cluster-wide IPAM, so every TMM gets a unique net1 from one pool:

```sh
curl -fsSLo whereabouts-ippools.yaml https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/whereabouts.cni.cncf.io_ippools.yaml
echo '8957e53f260e5ee0ad6560743d02c609ff21e15f6c3b23851971522b3bf6027a  whereabouts-ippools.yaml' | sha256sum -c -
curl -fsSLo whereabouts-reservations.yaml https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml
echo '59ff31b1cbe8ff7732160f3f533a71884c4bb03b18d2aa48c97b0a2aadcc4cb0  whereabouts-reservations.yaml' | sha256sum -c -
curl -fsSLo whereabouts-ds.yaml https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/daemonset-install.yaml
echo '4292b79c85115823b697d99171eb701783eaecf13c0eb8d6a6657f1c5d86b2eb  whereabouts-ds.yaml' | sha256sum -c -
sed -i 's|whereabouts:latest|whereabouts:v0.8.0|' whereabouts-ds.yaml   # upstream ships :latest
kubectl apply -f whereabouts-ippools.yaml -f whereabouts-reservations.yaml -f whereabouts-ds.yaml
kubectl -n kube-system rollout status daemonset/whereabouts --timeout=3m
```

On each node set `net.bridge.bridge-nf-call-iptables=0`, or Calico's natOutgoing MASQUERADE
breaks traffic on the NAD bridge.

The `bnk-bgp` NAD puts TMM's net1 on bridge `br-bnk-bgp`, which must reach your BGP peer at L2.
The OcNOS template peers **192.168.42.41** (AS 65001) from TMM AS 65000 — edit `neighbor` for your router.
Apply both **before** the CNEInstance so TMM boots with the final routing config:

```sh
kubectl apply -f 20-cne/01-nad-bnk-bgp.yaml -f 20-cne/02-ocnos-template.yaml
```

**CNEInstance** — FLO installs the BNK CRDs (License among them) in response to it:

```sh
kubectl apply -f 20-cne/03-cne-instance.yaml
kubectl wait --for=create crd/licenses.k8s.f5net.com --timeout=8m   # first pull of the manifest can be slow
kubectl wait --for=condition=Established crd/licenses.k8s.f5net.com --timeout=3m
```

**License** — skip if `kubectl -n f5-cne-core get license.k8s.f5net.com f5-cne-cluster-license` already reports Active: re-applying
makes CWC re-register, which fails on an already-registered asset. Retry the apply for ~100s if it
fails with "status unknown for quota" (CWC is still creating its ResourceQuota).

For a **test** JWT (`jku` on `product-tst…`): apply `20-cne/04-license-jwks-configmap.yaml` first and add to the License spec
`jwksConfigMapRef: {name: cwc-jwks-cm, key: key}` — CWC's built-in test certs expired. Production JWTs
need neither.

```sh
BNK_JWT="$(tr -d '\n' < keys/.jwt)" sh lib/render.sh 20-cne/04-license.yaml.tmpl | kubectl apply -f -
kubectl -n f5-cne-core wait --for=jsonpath='{.status.state}'=Active license.k8s.f5net.com/f5-cne-cluster-license --timeout=20m
```

**GatewayClass** — wait for Accepted before creating any Gateway, or the Gateway can stick at
"Waiting for controller":

```sh
kubectl apply -f 20-cne/05-gatewayclass.yaml
kubectl wait --for=condition=Accepted gatewayclass/bnk-gatewayclass --timeout=5m
```

**Infra** — the IPAM pool `bnk-dynamic-vips` (203.0.113.110–203.0.113.119) for address-less Gateways. Retry if the
`f5-single-infra-quota` is not populated yet:

```sh
kubectl apply -f 20-cne/06-infra.yaml
kubectl -n default wait --for=condition=Programmed infras.gateway.k8s.f5.com/infra --timeout=3m
```

**BGP bring-up** on every running TMM, once `daemonset/f5-tmm` has rolled out:

```sh
for pod in $(kubectl -n default get pod -l app=f5-tmm --field-selector=status.phase=Running -o name); do
  kubectl -n default exec $pod -c f5-tmm-routing -- sh -c 'echo '\''enable password 0 zebos'\'' > /config/zebos/rd0/passwd.conf'
  kubectl -n default exec $pod -c f5-tmm-routing -- sh -c 'printf '\''router bgp 65000\naddress-family ipv4 unicast\nredistribute kernel route-map RMALL\nredistribute connected route-map RMALL\nexit\nexit\n'\'' > /tmp/ocibnkctl-redist.cfg; imish -f /tmp/ocibnkctl-redist.cfg'
done
```

OcNOS only advertises once `redistribute` is re-issued at runtime, hence the second exec.

Done when `daemonset/f5-tmm` is ready on every `app=f5-tmm` node and every workload in
`f5-cne-core` is Available.

## scenarios

One directory per scenario, in dependency order; each one's README says what it checks. Most
assume the `bnk-gatewayclass` GatewayClass and VIPs in 203.0.113.0/24 advertised to a BGP peer —
adapt them for another cluster.

- **script** scenarios run by hand with `sh scenarios/<name>/apply.sh`, then `verify.sh`
  (one JSON line per check) and `cleanup.sh`. With no edge client, run verify with
  `EDGE_DRIVER=none` and the data-plane checks are skipped; `lib/edge.sh` explains how to
  point it at your own client and BGP router.
- **manifests** scenarios: `kubectl apply -f scenarios/<name>/manifests/` (files apply in name
  order), `kubectl delete -f` the same to remove; the checks live in `ocibnkctl` for now.

`ocibnkctl scenario run` runs the data-plane checks from inside the external FRR container
(`bnk-edge-frr-golden`), which learns the VIPs from TMM over BGP.

| Scenario | Rating | Form | Depends on | Title |
|---|---|---|---|---|
| [bgp-peer-frr](scenarios/bgp-peer-frr/README.md) | green | script | — | BGP peering: TMM OcNOS ⇄ external FRR (ToR) |
| [ai-inference-epp](scenarios/ai-inference-epp/README.md) | green | script | bgp-peer-frr | AI Inference Routing with the F5 Endpoint Picker — F5EPP + GAIE InferencePool, mock vLLM |
| [ai-semantic-cache](scenarios/ai-semantic-cache/README.md) | green | script | bgp-peer-frr | Semantic AI Model Caching (how-to #7) — k8s.f5.com/ai semantic_cache + sse-enabled |
| [ai-token-counting](scenarios/ai-token-counting/README.md) | green | script | bgp-peer-frr | Token Counting and Enforcement (how-to #6) — k8s.f5.com/ai-token-counting Gateway annotation |
| [ai-token-counting-dssm](scenarios/ai-token-counting-dssm/README.md) | green | script | bgp-peer-frr | AI Token Counting via custom iRule + DSSM/Redis — 4 LBs, streaming + non-streaming, persisted for tmm-stat-exporter |
| [bgp-anycast](scenarios/bgp-anycast/README.md) | green | script | bgp-peer-frr | BGP anycast all-active data plane (how-to #3) — every TMM advertises over the shared edge FRR |
| [cluster-wide-watch](scenarios/cluster-wide-watch/README.md) | green | script | — | Components needing cluster-wide access (how-to #2) — single controller reconciling a new namespace |
| [core-file-collection](scenarios/core-file-collection/README.md) | green | script | — | Core file collection (how-to #4) — CNEInstance.spec.coreCollection.enabled + CoreMond |
| [cwc-admin-access](scenarios/cwc-admin-access/README.md) | green | script | — | Restrict access to sensitive data (how-to #1) — bearer token + mTLS to CWC admin API |
| [external-resource-pool](scenarios/external-resource-pool/README.md) | green | script | bgp-peer-frr | Load Balance Traffic to External Resources (how-to #10) — Pool CR + HTTPRoute backendRef |
| [fic-dynamic-ip](scenarios/fic-dynamic-ip/README.md) | green | script | bgp-peer-frr | Dynamic IP address allocation (FIC for Gateway API) — address-less Gateway + GatewaySettings/Infra IPAM |
| [grpc-loadbalance](scenarios/grpc-loadbalance/README.md) | green | script | bgp-peer-frr | gRPC load balancing — L4Route (TCP) data plane; GRPCRoute control plane (kong/grpcbin) |
| [http-routing-e2e](scenarios/http-routing-e2e/README.md) | green | script | bgp-peer-frr | HTTP traffic steering with Gateway API HTTPRoute (how-to #8) — full data plane via NAD |
| [llm-gateway](scenarios/llm-gateway/README.md) | green | script | bgp-peer-frr | LLM gateway (BNK 2.4.1 AI gateway) — virtual keys, model scope, metering, console |
| [proxy-protocol-l4](scenarios/proxy-protocol-l4/README.md) | green | script | bgp-peer-frr | Proxy Protocol iRule on an L4 route (how-to #9) — F5BigCneIrule + L4Route + NetPolicy |
| [tcp-l4-loadbalance](scenarios/tcp-l4-loadbalance/README.md) | green | script | bgp-peer-frr | TCP load balancer via L4Route — weighted backends (70/30) |
| [udp-l4-loadbalance](scenarios/udp-l4-loadbalance/README.md) | green | script | bgp-peer-frr | UDP load balancer via L4Route — alpine/socat echo backend |
| [routing-template-multitenant](scenarios/routing-template-multitenant/README.md) | amber | manifests | — | Expose VLAN/Self-IPs in a RoutingTemplate (.vlans) — two tagged tenants on one interface |
| [routing-config-tenant](scenarios/routing-config-tenant/README.md) | amber | manifests | routing-template-multitenant | Per-tenant routing (RoutingConfig) — .tenant + per-tenant route domain in the template context |

## Registry login

`helm` pulls from `oci://repo.f5.com` need a login with the FAR credential:

```sh
tr -d '\n' < cne_pull_64.json | helm registry login repo.f5.com --username _json_key_base64 --password-stdin
```

`lib/render.sh` fills the `${NAME}` placeholders in the `.tmpl` files from environment
variables (POSIX sh + awk, also on macOS); see its header. The other `lib/` files are the
helpers the scenario scripts source.
