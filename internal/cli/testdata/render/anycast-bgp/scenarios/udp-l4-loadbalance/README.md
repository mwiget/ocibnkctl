# `udp-l4-loadbalance` — UDP load balancer via L4Route — alpine/socat echo backend

Rating: 🟢 &nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgp-peer-frr)

Exercises L4Route's UDP protocol support. The Gateway listens on
UDP 5005; an alpine/socat backend forks per datagram and replies
with a marker string. The verify step sends a probe via socat
from inside the FRR pod (which has a BGP-learned route to the
Gateway IP) and asserts the reply contains the marker.

UDP is connectionless and per-flow hashed by the L4 LB, so we
don't assert distribution across the two replicas — the point
here is that the data path works end-to-end.

Cleanup deletes the scn-udp-lb namespace.

## How to run

```bash
ocibnkctl scenario run udp-l4-loadbalance --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh), edge client:
[kit/lib/edge.sh](../../lib/edge.sh)):

```sh
sh kit/scenarios/udp-l4-loadbalance/apply.sh
EDGE_DRIVER=none sh kit/scenarios/udp-l4-loadbalance/verify.sh    # control plane only; data-plane checks skipped
sh kit/scenarios/udp-l4-loadbalance/cleanup.sh
```

Variables (VIP, ports, namespace) and their defaults are in
[`scenario.env`](scenario.env); set any of them in the environment.
