# Handoff: per-tenant VLAN self-IPs on BNK 2.4.0 GA

Investigation of the F5 design *"Expose VLAN/Self-IPs in Routing template"*
(`.vlans`) and the per-tenant data plane it implies, on a native-k3s demo-mode
cluster driven by `ocibnkctl`. Written for F5 engineering after the statement
"this is working": it separates what we verified end to end from what we could
not make pass, with the exact signatures, so the gap can be closed from either
side. Date: 2026-09-16.

## One-paragraph verdict

The `.vlans` feature **is in 2.4.0 GA and works**: per-TMM VLAN self-IPs
render into both global and tenant-scoped routing templates, keyed by
`F5SPKVlanSpec.name`, with all six documented fields. It is fed by reconciled
`F5SPKVlan`/`Vrf` CRs, which 2.4.0 only reconciles when the CNE controller
runs **without** `USE_GATEWAY_SETTINGS=true` (the legacy SPK network model).
Tenant VLANs, route domains and per-tenant next hops all program and answer on
the wire. What we could **not** achieve is a per-tenant **data path**: no CR
field puts a Gateway listener in a tenant VRF (every listener binds
`route-domain 0`), and an rd0 VIP fronting a `Pool{vrf: red}` member never
emits a backend SYN — the flow loops inside the tenant route domain. We need
one working reference for *client → VIP → member in tenant VRF*.

## Environment

| | ocibnkctl `mbpro-2-4-0` (system under test) | f5-bnk-udf kind lab (control) |
|---|---|---|
| Kubernetes | k3s v1.30.14+k3s1, 1 control + 1 worker, Calico | kind, 1 control + workers 1-4 |
| BNK | manifestVersion `2.4.0` | upgraded 2.2.0 → 2.4.0 in place |
| f5ingress | `v14.91.12-0.4.7` | `v14.91.12-0.4.7` (identical) |
| TMM | `tmm-img:v10.204.15-0.1.46`, `ocnos-img:v0.44.0-0.0.19`, `f5ing-tmm-pod-manager:v1.10.11`, FLO `v2.30.0-0.5.2` | same line |
| CNEInstance | `demoMode: true`, `wholeCluster: true`, `TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE`, `ZEBOS_STATE=ocnos`, `networkAttachments: [f5-tmm-dag]` | `demoMode: true`, mapres `TRUE`, `ZEBOS_STATE=legacy`, `networkAttachments: [external-net, egress-net]` |
| `USE_GATEWAY_SETTINGS` | `false` (legacy SPK model) | unset |
| TMM net1 | bridge CNI NAD `f5-tmm-dag` on Linux bridge `br-f5-tmm-dag` (vlan_filtering 0), host-local IPAM 192.0.2.0/24 | macvlan on eth1/eth2 |
| TMM interfaces | `0.1` = eth0 (Calico, TCL VLAN `eth0`), **`1.1` = net1, left bare for VLAN CRs** | `1.1`, `1.2` |
| License | Active (connected, paid) | Active |

The demo-mode caveat is real: TMM runs virtio inside the pod netns on a single
host, and the tenant "physical link" is a Linux bridge. Everything below that
concerns route domains is observed inside that TMM; if cross-route-domain
forwarding needs something this shape cannot provide, that is itself the
answer we need.

## Verified working

| Claim | Evidence |
|---|---|
| `USE_GATEWAY_SETTINGS` selects which network model reconciles; the two are mutually exclusive | Same binary, same cluster. With `true`: `F5SPKVlan`/`Vrf` accepted by API server + validation webhook, then **no** `.status`, no gRPC push, no `declTmm.vlan`, and no `vlan name not found` on TMM (it was never told). One VLAN sat status-less for 109 min, then reached `Programmed=True` within 1 s of the controller restarting with `false`. Control lab (no flag) shows the same CRs `Programmed` after its 2.4.0 upgrade with fresh `lastTransitionTime`. |
| `.vlans` is populated, per TMM, keyed by `F5SPKVlanSpec.name`, six fields | Rendered global context: `vlans:map[tenant-green:map[prefixLenV4:24 prefixLenV6:112 selfIPv4:198.51.101.11 selfIPv6:2001:db8:2::11 tag:2002 vrf:green] tenant-red:map[… selfIPv4:198.51.100.11 … tag:2001 vrf:red]]`. `selfip_v4s[i]` binds device `DP-i`. |
| Tenant-scoped render exposes `.tenant.spec.vrf` and a non-zero `.routeDomainID`; global does not | `RoutingConfig{vrf: red}` render: `routeDomainID:24195 … tenant:map[spec:map[templateRef:… vrf:red]]`; `ip vrf {{ .tenant.spec.vrf }}` → `ip vrf red`. The design doc's `ip vrf <no value>` comes from using `.tenant` in a **global** template. |
| `Vrf` CR → route domain, with `strict` and `interconnect_rdid` pushed | `declTmm.route_domain {"name":"red","rdid":24195,"strict":true}`; `interconnectVrf: ["default"]` → `"interconnect_rdid":[0]`; `["green"]` → `[55419]`. |
| Two tagged VLANs on one interface, each in its own VRF, self-IPs are real next hops | `vlan_member_stat`: `tenant-red 1.1`, `tenant-green 1.1`. From a peer in a private netns on 802.1Q subinterfaces: `arping` for `198.51.100.11` on tag 2001 → reply from TMM `aa:66:c1:e0:22:83`; for `198.51.101.11` on tag 2002 → reply; `198.51.101.11` on tag 2001 → **0 responses**. Captured tagged on the origin link and the bridge port. |
| `Pool.spec.vrf` places members in the tenant route domain | TMM: `pool member 198.51.100.254:8080 in route-domain 24195 in scn-dp-pool-pool-red-pool is up`; `pool_member_stat` address encodes the rd (`…C6:33:64:FE:83:5E:00:01`). |
| Full data path in the **default** VRF | Client `192.0.2.254` on the bridge, route `VIP/32 via 192.0.2.10` (TMM self-IP) → VS → HTTPRoute → backend → `200 OK` body. Works with an on-prefix VIP (`192.0.2.150`) and an off-prefix VIP (`203.0.113.150`, the BGP-announced shape), and with a Service or a default-VRF `Pool` backend. |

## Not working — the two gaps

### A. No way found to put a Gateway listener in a tenant VRF

Every virtual server binds `route-domain 0`
(`Listener … (0.0.0.0 -> 198.51.100.200:80 route-domain 0) bind successful`).
Attempts, all `Programmed=True` at the CR level:

| Attempt | Result |
|---|---|
| Gateway with static `addresses: [198.51.100.200]` inside the tenant-red subnet | VS created in rd0; VIP does not ARP on VLAN 2001; `clientside.pkts_in 0` |
| Annotation `k8s.f5.com/vlans-north: tenant-red` on the Gateway (the binary carries `"supported keys are 'k8s.f5.com/vxlans-north' or 'k8s.f5.com/vlans-north'"`) | No log line, no change; still rd0 |
| Gateway in a namespace that owns its own `Vrf` + tagged `F5SPKVlan` (`scn-crimson`, tag 2003, VIP `203.0.113.160`) | Bound `route-domain 0` |
| Infra model (`USE_GATEWAY_SETTINGS=true`): `Infra.spec.networks[]` tagged VLANs with `vrfRef`, `GatewaySettings.ingressConfig.defaultListenerNetwork{vrfRef, networkRefs, ipamRefs}`, address-less Gateway with `parametersRef` | VIP allocated from the tenant pool (`198.51.100.200`), `Programmed=True`; unreachable via the tenant self-IP **and** via the default self-IP; `pkts_in 0` |

`GatewaySettings…defaultListenerNetwork.vrfRef` is the only CRD field anywhere
that names a VRF for a listener, and it lives in the model that does not
reconcile `F5SPKVlan`/`.vlans`.

### B. rd0 VIP → `Pool{vrf: red}` member: the backend SYN never leaves TMM

Setup: VIP `203.0.113.150` (rd0), `HTTPRoute` rule → `Pool{vrf: red, members:
[198.51.100.254:8080]}`. The origin is in a **private netns** reachable only over
VLAN 2001 (leak check from the node netns: unreachable). Pool member reports
`up` in rd 24195.

Observed on one `curl -H 'Host: red.test' http://203.0.113.150/`:

- Client side: TMM completes the three-way handshake and receives the request
  (captured on TMM's net1 host-side veth).
- Server side: **no SYN** to `198.51.100.254` on the net1 host-side veth, on the
  Calico peer, or at the origin (tcpdump on the parent link and the tagged
  subinterface).
- `route_domain_stat` for `red` after that one request:
  `clientside.pkts_in 4 / 384 B`, `serverside.pkts_out 4 / 384 B`,
  `serverside.tot_conns 2` — the four SYN attempts re-enter the same route
  domain instead of leaving on VLAN 2001.
- Result: `curl: (28) Operation timed out … 0 bytes received`.

Variations, each on a **freshly created** `Vrf` (see gotcha below) with the
connected route present (`route_stat` link=1 for rd 24195):

| `Vrf` red | pushed as | outcome |
|---|---|---|
| `strictIsolation: true` | `"strict":true` | timeout, same counters |
| `strictIsolation: false` | (no strict) | timeout, same counters |
| `strictIsolation: true, interconnectVrf: ["default"]` | `"strict":true,"interconnect_rdid":[0]` | timeout, same counters |
| control: `Pool` without `vrf`, same member address | member in rd0, reachable only through the pod network, so not a valid tenant path | — |

## Bugs and gotchas found on the way

1. **`RoutingConfig` panics the controller under `USE_GATEWAY_SETTINGS=true`** with
   a VRF from `Infra.spec.vrfs`:
   `panic: runtime error: invalid memory address or nil pointer dereference` in
   `internal/k8s.(*LoadBalancerController).RoutingConfigReconciler`
   (`controller_routingconfig.go:562` ← `syncRoutingConfig`:178 ← `sync`:4503).
   `f5-cne-controller` crash-loops and `f5-validation-svc` goes down with it, so
   every F5 CR apply fails admission with `502 Bad Gateway`. The same CR under
   the legacy model is handled cleanly.
2. **Patching a live `Vrf`** (`strictIsolation` or `interconnectVrf`) re-creates
   the route domain and drops its connected route (`route_stat` link 0 for that
   rd) until the VLAN is re-applied. Delete + recreate both instead.
3. `GlobalRoutingConfig` is a **cluster singleton** ("Only one
   GlobalRoutingConfig instance is allowed"); `RoutingTemplate` and
   `GlobalRoutingConfig` are **immutable** (delete + recreate).
4. `RoutingConfig` is only reconciled in the BNK namespace; the same CR in
   another namespace draws no webhook, no status, no log line.
5. The routing-template renderer is plain `text/template` — no Sprig. A
   `list`/`dict` call fails the whole render (`function "list" not defined`) and
   leaves TMM unconfigured.
6. Under the other model, `F5SPKVlan` shows `READY` blank **forever** with no
   error anywhere — an operator following "Configure the Network" gets no
   signal that the CR is inert.
7. `F5SPKVlan` IPv6 pattern rejects non-hex labels (`2001:db8:red::11`); the
   CRD doc page for `F5SPKVlan` does not document the live `vrf` field.

## Questions for engineering

1. In the legacy SPK model, which CR/field binds a **Gateway listener** to a
   tenant VRF? If none by design, is the intended shape "VIP in the default rd,
   members in tenant rds" — and what makes that cross-rd path work?
2. For an rd0 VIP → `Pool{vrf: X}` member, what is required for TMM to source
   the server-side connection from the tenant VLAN? Is an
   `F5SPKSnatpool{vrf: X}` mandatory (we used SNAT automap)? What should
   `interconnectVrf` reference for the default route domain — `"default"` is
   accepted and pushed as `interconnect_rdid: [0]` with no effect.
3. Is `k8s.f5.com/vlans-north` supported on a Gateway in 2.4.0, and in what
   format?
4. Could we get one working manifest set for
   *client → VIP (announced by BGP, off the self-IP prefixes) → member in a
   tenant VRF*, including the `Vrf`/`F5SPKVlan`/`Pool`/snatpool CRs?

## Reproduce it

Scenarios (require `bnk.tmm_dataplane_mode: selfip-dag`, which renders
`USE_GATEWAY_SETTINGS=false`):

```bash
ocibnkctl scenario run routing-template-multitenant --poc <poc> --verbose  # .vlans + ARP next-hop probe
ocibnkctl scenario run routing-config-tenant        --poc <poc> --verbose  # .tenant + tenant route domain
```

The data-plane rig used for gap B (all in the worker node's netns, via
`docker run --network container:<node> --privileged nicolaka/netshoot`):

```sh
# client leg, untagged, default VRF side
ip link add scn-cli type veth peer name scn-cli-br; ip link set scn-cli-br master br-f5-tmm-dag up
ip link set scn-cli up; ip addr add 192.0.2.254/24 dev scn-cli
ip route replace 203.0.113.150/32 via 192.0.2.10 dev scn-cli        # VIP via TMM self-IP
# origin leg, PRIVATE netns, reachable only over the tagged tenant VLANs
ip netns add origin; ip link add scn-org type veth peer name scn-org-br
ip link set scn-org-br master br-f5-tmm-dag up; ip link set scn-org netns origin
ip netns exec origin sh -c 'ip link set lo up; ip link set scn-org up;
  ip link add link scn-org name scn-org.2001 type vlan id 2001; ip link add link scn-org name scn-org.2002 type vlan id 2002;
  ip addr add 198.51.100.254/24 dev scn-org.2001; ip addr add 198.51.101.254/24 dev scn-org.2002;
  ip link set scn-org.2001 up; ip link set scn-org.2002 up'
ip netns exec origin sh -c 'cd /srv/red && python3 -m http.server 8080 --bind 198.51.100.254 &'
curl -H 'Host: red.test' http://203.0.113.150/
```

Objects as applied (Vrf, F5SPKVlan, Pool, Gateway, HTTPRoute) and rig notes:
`scratchpad/tenant-dataplane-repro/{cluster-objects.yaml,README.md}` from the
session; the same objects are rendered by the scenarios under
`artifacts/scenarios/routing-template-multitenant/`.

## Where things stand in the repo (uncommitted)

- `USE_GATEWAY_SETTINGS` now follows the dataplane mode (`selfip-dag` → false,
  else true); `deploy cne` waits for the `F5SPKVlan` to reach `Programmed`
  instead of printing "N TMM(s) active" unconditionally, and skips the Infra CR
  in the legacy model.
- Scenarios `routing-template-multitenant` (`.vlans`, six fields, per-TMM, ARP
  next-hop + isolation probe) and `routing-config-tenant` (`.tenant`,
  per-tenant `routeDomainID`) — both pass, rated amber because they need
  `selfip-dag`; the ten edge/BGP scenarios and `fic-dynamic-ip` now fail fast
  with the required mode instead of timing out.
- `docs/bnk-2.4.0-doc-errata.md` #26: the mode switch, the inert-CR symptom,
  the `.vlans` dependency and the `RoutingConfig` panic.

## Cluster state left for the repro

`mbpro-2-4-0` is in `selfip-dag`, `USE_GATEWAY_SETTINGS=false`: `Vrf` red
(strict false) / green (strict true); `F5SPKVlan` `f5-tmm-dag` (tag 0),
`tenant-red` (2001), `tenant-green` (2002) on `1.1`; namespace `scn-dp` with
Gateways `gw-vip` (203.0.113.150), `gw-red`, `gw-default`, HTTPRoutes
`rt-tenants`, `rt-red`, Pools `pool-red`, `pool-green`; container `scn-origins`
still serving both tenant origins. `~/.kube/config` points at this cluster;
`kind export kubeconfig --name bnk` restores the kind lab context.
