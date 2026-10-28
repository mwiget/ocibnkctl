# `tcp-l4-loadbalance` — TCP load balancer via L4Route — weighted backends (70/30)

Rating: 🟢 &nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgp-peer-frr)

Exercises L4Route's load-balancing behavior with two weighted
backends (70/30). Each backend is a separate nginx Deployment
serving a distinct marker body (backend=A / backend=B), so the
verify step can identify which backend served each request.

Verification issues 20 curls from inside the FRR pod through the
Gateway's TCP listener at 203.0.113.106:8080. Pass requires:
  - Gateway Programmed=True
  - L4Route Accepted=True
  - FRR has 203.0.113.106/32 in its BGP table
  - Both backend=A and backend=B served at least one request

Exact weighting (70/30) is not asserted — 20 samples is far too
small to call that reliably. The point is that the L4Route weight
field actually splits traffic across multiple endpoints, not just
hashes to one.

Cleanup deletes the scn-tcp-lb namespace.

## How to run

```bash
ocibnkctl scenario run tcp-l4-loadbalance --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh), edge client:
[kit/lib/edge.sh](../../lib/edge.sh)):

```sh
sh kit/scenarios/tcp-l4-loadbalance/apply.sh
EDGE_DRIVER=none sh kit/scenarios/tcp-l4-loadbalance/verify.sh    # control plane only; data-plane checks skipped
sh kit/scenarios/tcp-l4-loadbalance/cleanup.sh
```

Variables (VIP, ports, namespace) and their defaults are in
[`scenario.env`](scenario.env); set any of them in the environment.
