# `bgp-peer-frr` — BGP between every TMM and the edge router

F5 how-to: [Set up dynamic routing with BGP](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/spk-zebos-config.html)
&nbsp;·&nbsp; Rating: 🟢

Foundation scenario: every other data-plane scenario reaches its Gateway
VIPs over the routes TMM advertises here.

In the `anycast-bgp` data plane, `ocibnkctl deploy cne` already gives every
TMM a net1 on the `bnk-bgp` NAD (whereabouts IPAM) and an OcNOS routing
template that peers the edge router — on an ocibnkctl cluster the external
FRR container `bnk-edge-frr-<cluster>` (AS 65001; TMM is AS 65000). This
scenario makes the session useful and checks it:

- `apply.sh` writes OcNOS's `passwd.conf` on every TMM (imish refuses
  commands without it) and re-issues the BGP `redistribute` statements
  ([`lib/ocnos-redistribute.cfg`](../../lib/ocnos-redistribute.cfg)) —
  OcNOS only redistributes when they are issued at runtime.
- `verify.sh` polls the router until every TMM's session is Established
  and its BGP table holds routes learned from TMM.

FRR's `show ip bgp summary` shows an Established session as an Up/Down
timer (`00:02:13`) rather than the word "Established"; `verify.sh` counts
those rows on the edge L2 (`EDGE_PREFIX`).

The scenario creates no Kubernetes objects; cleanup has nothing to remove.

## How to run

```bash
ocibnkctl scenario run bgp-peer-frr --poc <pocdir>
```

Or by hand on any cluster running the anycast-bgp data plane, pointing
`EDGE_VTYSH` at your router's CLI ([`lib/edge.sh`](../../lib/edge.sh)):

```sh
sh kit/scenarios/bgp-peer-frr/apply.sh
EDGE_DRIVER=external EDGE_VTYSH="ssh my-router vtysh -c" EDGE_PREFIX=10.1.2. \
  sh kit/scenarios/bgp-peer-frr/verify.sh
```

With `EDGE_DRIVER=none` (no router CLI) both checks are reported skipped.
`apply.sh` fails fast unless the CNEInstance runs anycast-bgp (a NAD on TMM
and `TMM_MAPRES_ADDL_VETHS_ON_DP=FALSE`): in the other modes mapres takes
net1 for the data plane and flushes its kernel IP, so OcNOS has no address
to peer from.

## Verification

```
✓ external FRR shows all 1 TMM neighbor(s) Established  (1/1 established)
✓ external FRR learned routes from TMM (BGP RIB non-empty)
```

The Mermaid sketch in [README.md "Network topology"](../../../README.md#network-topology)
shows how TMM, the bnk-bgp bridge and the edge router are wired.
