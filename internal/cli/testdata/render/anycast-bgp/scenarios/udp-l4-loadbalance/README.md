# udp-l4-loadbalance

UDP load balancer via L4Route — alpine/socat echo backend

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Exercises L4Route's UDP protocol support. The Gateway listens on
UDP 5005; an alpine/socat backend forks per datagram and replies
with a marker string. The verify step sends a probe via socat
from inside the FRR pod (which has a BGP-learned route to the
Gateway IP) and asserts the reply contains the marker.

UDP is connectionless and per-flow hashed by the L4 LB, so we
don't assert distribution across the two replicas — the point
here is that the data path works end-to-end.

Cleanup deletes the scn-udp-lb namespace.

## Manifests

- `manifests/01-namespace.yaml`
- `manifests/03-backend.yaml`
- `manifests/04-gateway.yaml`
- `manifests/05-l4route.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
