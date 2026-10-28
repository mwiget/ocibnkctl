# tcp-l4-loadbalance

TCP load balancer via L4Route — weighted backends (70/30)

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

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

## Manifests

- `01-namespace.yaml`
- `03-backend.yaml`
- `04-gateway.yaml`
- `05-l4route.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
