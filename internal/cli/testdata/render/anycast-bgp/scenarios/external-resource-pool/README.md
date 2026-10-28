# external-resource-pool

Load Balance Traffic to External Resources (how-to #10) — Pool CR + HTTPRoute backendRef

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Demonstrates BNK's Pool CR as an HTTPRoute backend, the BNK-native
way to load-balance to endpoints that aren't k8s Services.

Production BNK uses Pool to point at out-of-cluster resources
(VMs, other clusters, bare-metal hosts). On kind we stand in
with an nginx pod attached to the bnk-bgp Multus NAD — TMM
already has a net1 interface on that bridge (from the
bgp-peer-frr setup), so the backend pod's NAD IP is reachable
from TMM at L2.

Pipeline:
  - Gateway with static spec.addresses .101 (F5BnkGateway pools were dropped in BNK 2.4)
  - ext-backend Deployment (nginx on bnk-bgp NAD)
  - Gateway with spec.addresses=[203.0.113.101]
  - HTTPRoute hostname=extres.ocibnkctl.local, backendRefs to
    {group:k8s.f5net.com, kind:Pool, name:ext-backend-pool}
  - Pool CR with one member at the discovered backend NAD IP

Verification curls from inside the FRR pod (same client used by
http-routing-e2e): the kernel route to 203.0.113.101/32 lands
via BGP+net1, TMM proxies through to the Pool member IP. No
new client plumbing.

Cleanup deletes the scn-extres namespace; cluster-wide
GatewayClass + bnk-bgp NAD stay (reused by other scenarios).

## Manifests

- `manifests/01-namespace.yaml`
- `manifests/03-backend.yaml`
- `manifests/04-gateway.yaml`
- `manifests/05-pool.yaml.tmpl`
- `manifests/06-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.

`manifests/05-pool.yaml.tmpl` holds `${NAME}` placeholders that `ocibnkctl` fills from live
cluster state at apply time (e.g. a Service ClusterIP). From this directory, fill them with
`NAME=<value> sh ../../lib/render.sh manifests/05-pool.yaml.tmpl | kubectl apply -f -`. `kubectl apply
-f` on the directory skips `.tmpl` files.
