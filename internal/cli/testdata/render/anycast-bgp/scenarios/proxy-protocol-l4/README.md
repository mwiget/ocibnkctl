# proxy-protocol-l4

Proxy Protocol iRule on an L4 route (how-to #9) — F5BigCneIrule + L4Route + NetPolicy

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Demonstrates BNK's PROXY-protocol iRule pattern on a TCP route.

Three new BNK CRs come together:

  - F5BigCneIrule  — the iRule TCL script. On CLIENT_ACCEPTED it
    captures the original client IP+port; on SERVER_CONNECTED it
    prepends a PROXY v1 line to the server-side payload via
    TCP::respond.
  - L4Route        — TCP-protocol route binding a Gateway listener
    to a backend Service (analogous to HTTPRoute but for raw L4).
    Critically sets spec.pvaAccelerationMode=disabled so the data
    path stays in TMM's TCL/iRule slow path. With the default
    full/assisted PVA mode, TMM hardware-offloads the connection
    after handshake and the iRule's TCP::respond fires in the VM
    but cannot inject bytes onto the offloaded wire.
  - NetPolicy   — wires the iRule (extensionRef) to the route
    (targetRef) so the iRule fires on this route's traffic.

The nginx backend has 'listen 80 proxy_protocol' configured, so
it consumes the PROXY header and exposes the original client
address as $proxy_protocol_addr — the response body echoes that
value, making end-to-end PROXY plumbing easy to assert.

5/5 curls from FRR through the Gateway are expected to return
'ocibnkctl-scenario-proxy-protocol-OK proxy_addr=<frr-net1-ip>'.
A response with proxy_addr=0.0.0.0 (or no proxy_addr at all)
indicates the iRule fired but TCP::respond did not inject —
verify pvaAccelerationMode actually landed as 'disabled' in
the L4Route spec and TMM's profile_bigproto.

Cleanup deletes scn-proxy namespace.

## Manifests

- `manifests/01-namespace.yaml`
- `manifests/03-backend.yaml`
- `manifests/04-gateway.yaml`
- `manifests/05-irule.yaml`
- `manifests/06-l4route.yaml`
- `manifests/07-bnknetpolicy.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
