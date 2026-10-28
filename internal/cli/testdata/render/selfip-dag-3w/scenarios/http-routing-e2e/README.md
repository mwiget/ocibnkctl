# http-routing-e2e

HTTP traffic steering with Gateway API HTTPRoute (how-to #8) — full data plane via NAD

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

End-to-end HTTPRoute scenario with real data-plane traffic.

Requires bgp-peer-frr to be running already — relies on the
FRR pod in scn-bgp having a BGP session with TMM over the
bnk-bgp NAD. When this scenario applies the Gateway with
static spec.addresses=203.0.113.100, TMM (via ZeBOS's
redistribute kernel) advertises that /32 over BGP. FRR
receives it with next-hop set to TMM's net1 IP and installs
a kernel route for 203.0.113.100/32 via net1.

The verify step then execs 5 curls from inside the FRR pod
to http://203.0.113.100/ with the configured hostname. The
path is: FRR socket → FRR kernel routing → net1 → bnk-bgp
bridge → TMM net1 → TMM Gateway listener → nginx backend.
TMM's eth0 TCP hook is completely bypassed because the
traffic never touches eth0.

No separate curl-client pod is deployed — the FRR pod is
already exactly the kind of client this scenario needs
(on the NAD, with the BGP-learned route to the Gateway IP).
Operators can reproduce manually:

    kubectl -n scn-bgp exec deploy/scn-frr -c frr -- \
      curl -sS -H 'Host: ocibnkctl.local' http://203.0.113.100/

Cleanup: delete the scn-httproute-e2e namespace. The
GatewayClass stays (cluster-wide; reused by other scenarios).

## Manifests

- `01-gatewayclass.yaml`
- `02-namespace.yaml`
- `04-backend.yaml`
- `05-gateway.yaml`
- `06-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
