# `external-resource-pool` — BNK Pool CR as an HTTPRoute backend

F5 how-to: [Configure Load Balance Traffic to External Resources](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html)
&nbsp;·&nbsp; Rating: 🟢
&nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgp-peer-frr)
&nbsp;·&nbsp; Wall time: **~14s** (no TMM restart)

The novelty over `http-routing-e2e` is the BNK
**`Pool`** CR (`group: k8s.f5net.com, kind: Pool`). HTTPRoute's
`backendRefs` points at the Pool, not at a Service.
`Pool.spec.members` lists endpoints by `address` + `port` —
production BNK uses this for VMs / bare-metal / other clusters
that aren't in the local Service registry.

On kind we simulate "external" with a plain Calico nginx pod;
the Pool member references its `status.podIP`. TMM reaches it
via normal cross-pod Kubernetes networking, exactly what BNK
does in production when a Pool member is any reachable IP.
(Earlier iterations attached the backend to the bnk-bgp NAD,
but that just coupled the backend to the same node as TMM
without adding capability.)

## Manifests

| File | What it is |
|---|---|
| [`01-namespace.yaml`](manifests/01-namespace.yaml) | `scn-extres` |
| [`03-backend.yaml`](manifests/03-backend.yaml) | nginx Deployment + Service + ConfigMap (marker body) — plain Calico pod |
| [`04-gateway.yaml.tmpl`](manifests/04-gateway.yaml.tmpl) | Gateway with `spec.addresses=${VIP}` (default 203.0.113.101), HTTP listener |
| [`05-pool.yaml.tmpl`](manifests/05-pool.yaml.tmpl) | template — Pool CR with `${BACKEND_IP}` filled at apply time from `ext-backend`'s `status.podIP` |
| [`06-httproute.yaml`](manifests/06-httproute.yaml) | HTTPRoute hostname `extres.ocibnkctl.local`, `backendRefs` → Pool CR |

## How to run

```bash
ocibnkctl scenario run bgp-peer-frr           --poc <pocdir>   # if not running
ocibnkctl scenario run external-resource-pool --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh), edge client:
[kit/lib/edge.sh](../../lib/edge.sh)):

```sh
sh kit/scenarios/external-resource-pool/apply.sh
EDGE_DRIVER=none sh kit/scenarios/external-resource-pool/verify.sh    # control plane only; data-plane checks skipped
sh kit/scenarios/external-resource-pool/cleanup.sh
```

Variables and their defaults are in [`scenario.env`](scenario.env); set any
of them in the environment.

## Verification

```
✓ ext-backend Deployment Available
✓ Gateway Programmed=True
✓ HTTPRoute Accepted=True
✓ Pool CR has at least one member entry on :80
✓ FRR BGP table has 203.0.113.101/32 advertised by TMM
✓ 5/5 curls via Gateway return ext-backend marker body
```

Reproduce manually:

```bash
docker run --rm --network container:bnk-edge-frr-<cluster> nicolaka/netshoot \
  curl -sS -H 'Host: extres.ocibnkctl.local' http://203.0.113.101/
# → ocibnkctl-scenario-extres-pool-OK
```

## Cleanup

`ocibnkctl scenario clean external-resource-pool` deletes the
`scn-extres` namespace. GatewayClass + `bnk-bgp` NAD stay
cluster-wide (reused by other scenarios).
