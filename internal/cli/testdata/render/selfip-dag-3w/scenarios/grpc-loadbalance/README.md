# grpc-loadbalance

gRPC load balancing — L4Route (TCP) data plane; GRPCRoute control plane (kong/grpcbin)

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Load-balances gRPC to a kong/grpcbin backend in the 2-node demo-TMM
shape, and exercises BNK's GRPCRoute CRD alongside it.

Two data paths are deployed for the same grpcbin pods:

  1. GRPCRoute (Gateway API L7), HTTP listener on port 50051 — the
     CRD this how-to is about. Single rule, no hostnames/matches/
     filters (the BNK docs note those are unsupported).
  2. L4Route (TCP) on port 50052 — a raw L4 path to a plain-TCP
     copy of the Service.

Why two paths (🟢 green via the L4Route):
  - GRPCRoute control plane reconciles cleanly — Gateway
    Programmed=True, GRPCRoute Accepted=True, pool member Up — but
    its DATA plane fails: grpcurl through the L7 Gateway returns
    "rpc error: code = Internal desc = stream terminated by
    RST_STREAM with error code: INTERNAL_ERROR". TMM's FLO applies
    the profile-http + profile-json + profile-httprouter chain to
    every GRPCRoute virtual server, which corrupts HTTP/2 binary
    frames. This is a BNK 2.3.0 FLO limitation (no raw HTTP/2
    passthrough mode for GRPCRoute). The scenario keeps this call
    as an INFORMATIONAL assertion so the report shows the
    RST_STREAM verbatim.
  - The L4Route path carries gRPC end-to-end: grpcurl reflection
    list AND a real unary grpcbin.GRPCBin/Index call both succeed
    through the L4 Gateway IP. Proven by experiment to be a true
    fix (not PVA-related — it works at pvaAccelerationMode
    full/assisted, the default, same as tcp-l4-loadbalance). The
    trade-off: L4Route is opaque TCP LB — no gRPC/HTTP-2-aware
    per-method routing — but it carries HTTP/2 intact, so gRPC
    load balancing IS testable green in this shape today.

Note: the L4Route binds a plain-TCP Service. An L4Route refuses a
Service tagged appProtocol: kubernetes.io/h2c (ResolvedRefs=False /
UnsupportedProtocol), so the L4 path uses its own untagged Service.

The pinned grpcurl binary (v1.9.3, SHA-256 verified) installs into
the FRR pod over the Multus NAD with a BGP-learned route to each
Gateway IP. Full analysis: docs/grpc-route-investigation.md.

Cleanup deletes the scn-grpc namespace.

## Manifests

- `01-namespace.yaml`
- `03-backend.yaml`
- `04-gateway.yaml`
- `05-grpcroute.yaml`
- `06-l4-backend-svc.yaml`
- `07-l4-gateway.yaml`
- `08-l4route.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
