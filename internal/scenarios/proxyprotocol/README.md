# `proxy-protocol-l4` — F5BigCneIrule + L4Route + BNKNetPolicy

F5 how-to: [Proxy Protocol iRule support for L4 routes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html)
&nbsp;·&nbsp; Rating: 🟢
&nbsp;·&nbsp; Depends on: [`bgp-peer-frr`](../bgppeer)
&nbsp;·&nbsp; Wall time: **~24s**

Demonstrates BNK's PROXY-protocol iRule pattern on a TCP route.
Three new BNK CRs come together:

- **`F5BigCneIrule`** — the iRule TCL script. On `CLIENT_ACCEPTED`
  captures `[IP::client_addr]` + `[TCP::client_port]`; on
  `SERVER_CONNECTED` prepends a PROXY v1 line to the server-side
  payload via `TCP::respond`.
- **`L4Route`** (`gateway.k8s.f5net.com/v1`) — TCP-protocol route
  binding a Gateway listener to a backend Service (analogous to
  HTTPRoute but for raw L4). **Sets `spec.pvaAccelerationMode:
  disabled`** — the load-bearing knob, see below.
- **`BNKNetPolicy`** (`gateway.k8s.f5net.com/v1alpha1`) — wires
  the iRule (`extensionRefs`) to the Gateway listener
  (`targetRefs`) so the iRule fires on that listener's traffic.

The nginx backend has `listen 80 proxy_protocol` configured so
it parses the PROXY header and exposes the original client
address as `$proxy_protocol_addr` — the response body echoes
that value, making end-to-end PROXY plumbing easy to assert.

## The `pvaAccelerationMode: disabled` knob

PVA (Packet Velocity Accelerator) is TMM's hardware-offload
fast path. With the L4Route default of `full/assisted`, TMM
hardware-offloads the connection after handshake and **the
iRule's `TCP::respond` fires in the TCL VM but cannot inject
bytes onto the offloaded wire**. Symptom:

```
nginx logs:  broken header: "GET / HTTP/1.1" while reading PROXY protocol
curl:        curl: (52) Empty reply from server
```

…even though TMM logs show the iRule firing all the way through:

```
Rule scn-proxy-pp-prepend <CLIENT_ACCEPTED>:  encoded N bytes for client …
Rule scn-proxy-pp-prepend <SERVER_CONNECTED>: TCP::respond returned (no error)
```

Setting `pvaAccelerationMode: disabled` keeps the data path
in TMM's slow path where the iRule has full wire access; the
PROXY v1 line then prepends on every server-side connection
and nginx parses it correctly:

```
$ curl http://203.0.113.102:8000/
ocibnkctl-scenario-proxy-protocol-OK proxy_addr=192.168.99.20
```

`proxy_addr=192.168.99.20` is nginx echoing the parsed
PROXY-protocol source IP — and `192.168.99.20` is FRR's NAD
IP, the actual client TMM saw. End-to-end PROXY pipeline:
FRR (client) → TMM (iRule prepends PROXY v1) → nginx (parses
PROXY, echoes `$proxy_protocol_addr` in response body).

### How we found this

This scenario shipped amber for ~24 hours while we worked through
a long false-trail diagnosis:

- Tried both PROXY v1 (text) and v2 (verbatim F5 DevCentral
  initiator iRule, binary encoding) — both fired in the VM
  according to TMM logs but neither reached the wire.
- Tried alternate iRule events (LB_SELECTED, serverside { },
  CLIENT_DATA with TCP::collect + TCP::payload replace + release)
  — same outcome.
- Concluded that `TCP::respond` was a runtime stub in BNK 2.3's
  `profile_bigproto` and that lifting to green needed F5 to
  implement an injection primitive.

That conclusion was wrong. `TCP::respond` works fine — PVA
acceleration was eating the bytes. Once an internal F5 note
surfaced that `pvaAccelerationMode` should be `disabled` for
iRule-bearing L4Routes, a one-line patch on the L4Route fixed
all five test curls.

## Manifests

| File | What it is |
|---|---|
| [`01-namespace.yaml`](manifests/01-namespace.yaml) | `scn-proxy` |
| [`02-bnkgateway.yaml`](manifests/02-bnkgateway.yaml) | `F5BnkGateway` IP pool for 203.0.113.102 |
| [`03-backend.yaml`](manifests/03-backend.yaml) | nginx Deployment + Service + ConfigMap; `listen 80 proxy_protocol` so it parses the PROXY header (and rejects plain HTTP) |
| [`04-gateway.yaml`](manifests/04-gateway.yaml) | Gateway with TCP listener on port 8000, `allowedRoutes.kinds = L4Route` |
| [`05-irule.yaml`](manifests/05-irule.yaml) | `F5BigCneIrule` with the PROXY v1 iRule TCL |
| [`06-l4route.yaml`](manifests/06-l4route.yaml) | `L4Route` binding the listener to the backend Service, **with `pvaAccelerationMode: disabled`** |
| [`07-bnknetpolicy.yaml`](manifests/07-bnknetpolicy.yaml) | `BNKNetPolicy` linking iRule → Gateway listener (kind=Gateway is the only allowed `targetRefs.kind`; sectionName=tcp-listener) |

## How to run

```bash
ocibnkctl scenario run bgp-peer-frr     --poc <pocdir>   # if not running
ocibnkctl scenario run proxy-protocol-l4 --poc <pocdir>
```

## Verification (7/7)

```
✓ pp-backend Deployment Available
✓ Gateway Programmed=True
✓ L4Route Accepted=True
✓ F5BigCneIrule pp-prepend exists
✓ BnkNetPolicy scn-proxy-attach exists
✓ FRR BGP table has 203.0.113.102/32 advertised by TMM
✓ 5/5 L4 curls carry PROXY header parsed by nginx
```

Reproduce the data-plane check manually:

```bash
kubectl -n scn-bgp exec deploy/scn-frr -c frr -- \
  curl -sS --fail http://203.0.113.102:8000/
# → ocibnkctl-scenario-proxy-protocol-OK proxy_addr=192.168.99.20
```

## Cleanup

`ocibnkctl scenario clean proxy-protocol-l4` deletes the
`scn-proxy` namespace.
