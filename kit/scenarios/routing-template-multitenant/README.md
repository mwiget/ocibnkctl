# `routing-template-multitenant` — VLAN self-IPs in a RoutingTemplate (`.vlans`)

F5 sources: design doc *"Expose VLAN/Self-IPs in Routing template"*
&nbsp;·&nbsp; [F5SPKVlan CRD](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-vlan-crd.html)
&nbsp;·&nbsp; [Configure global BGP routing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/configure-global-BGP-routing.html)
&nbsp;·&nbsp; Rating: 🟡 (all assertions pass, but only in `selfip-dag` mode — so it is excluded from `scenario run --all`)
&nbsp;·&nbsp; Depends on: nothing

VLAN self-IPs are allocated **per TMM** by the controller
(`assignSelfIPPerGroupPerDeviceName` → `groupDeviceNameSelfIPV4Map`). The
feature under test exposes them to template authors through a new top-level
`.vlans` variable, following the existing `.global.tmmAllocatedIPs` pattern:

```
.vlans.<name>.selfIPv4 / .selfIPv6 / .prefixLenV4 / .prefixLenV6 / .tag / .vrf
```

keyed by `F5SPKVlanSpec.name`. Each TMM sees only **its own** self-IP.

## The multi-tenant shape

Two tagged VLANs on the **same** TMM interface (`1.1`), one per tenant:

| VLAN | tag | vrf | self-IPs (one per TMM) |
|---|---|---|---|
| `tenant-red` | 2001 | `red` | `198.51.100.11`, `.12`, … |
| `tenant-green` | 2002 | `green` | `198.51.101.11`, `.12`, … |

Each tenant gets its **own subnet**, not two ranges in one `/24` — that is what
lets the next-hop probe attribute a reply to a VLAN.

One `RoutingTemplate` then renders both tenants' config from `.vlans`, and the
controller renders it **once per TMM device** with that device's own
addresses — so the operator never enumerates per-node IPs in the template.

Live render on a 2.4.0 GA cluster:

```
! OCIBNKCTL-VLAN tenant-red   selfIPv4=198.51.100.11 selfIPv6=2001:db8:1::11 prefixLenV4=24 prefixLenV6=112 tag=2001 vrf=red
! OCIBNKCTL-VLAN tenant-green selfIPv4=198.51.100.21 selfIPv6=2001:db8:1::21 prefixLenV4=24 prefixLenV6=112 tag=2002 vrf=green
```

## What it asserts

| # | Assertion |
|---|---|
| 1-2 | both `Vrf` CRs `Programmed=True` |
| 3-4 | both `F5SPKVlan`s `Programmed=True` ("CR config sent to all grpc endpoints") |
| 5 | TMM bound **both** VLANs to interface `1.1` (`tmctl vlan_member_stat`) |
| 6 | the template context exposes `.vlans` |
| 7+ | per TMM, per tenant: **all six** fields correct, `selfIPv4` = that device's own address |
| 8+ | tenant isolation in the render — red never carries green's address, and vice versa |
| 9 | a tenant VLAN peer is built in the node's netns |
| 10+ | **each tenant self-IP answers ARP on its own VLAN** — it is a real next hop |
| 11 | **green's self-IP is SILENT on red's VLAN** — tenant isolation on the wire |

Assertion 7 is the load-bearing control-plane one: `selfip_v4s` element order
maps to `DP-0`, `DP-1`, … and per-device rendering is invisible from CR status
— you have to read the rendered file back out of each TMM pod.

## The next-hop probe (assertions 9-11)

Rendering the right address proves the control plane and nothing else. To show
a tenant self-IP is a **real next hop**, the scenario puts a peer on the tenant
VLANs: a veth into `br-f5-tmm-dag` (the per-node bridge TMM's interface `1.1`
sits on) carrying one 802.1Q subinterface per tag, addressed `.254` in each
tenant subnet. Linux bridges don't filter VLANs by default, so tagged frames
reach `1.1` and are matched by the F5SPKVlan with that tag.

```
✓ tenant-red   198.51.100.11 answers ARP on VLAN 2001
    Unicast reply from 198.51.100.11 [AA:66:C1:E0:22:83] 2.065ms
✓ tenant-green 198.51.101.11 answers ARP on VLAN 2002
    Unicast reply from 198.51.101.11 [AA:66:C1:E0:22:83] 1.868ms
✓ tenant-green 198.51.101.11 is SILENT on tenant-red's VLAN 2001
    Sent 3 probes (3 broadcast(s))  Received 0 response(s)
```

Both tenants answer from the same MAC — one physical interface, separated by
VLAN tag and route domain, which is exactly the shape under test.

**ARP, not ICMP, on purpose:** a self-IP answers ARP unconditionally because it
must in order to receive any traffic, whereas ICMP is subject to the VLAN's
`allowed_services` lockdown (empty by default here). A ping-based probe would
report a perfectly working self-IP as dead. `arping` also exits non-zero when
nothing answers — which is the *expected* result for the isolation probe — so
the assertion reads the reply count, not the exit status.

The probe interfaces live in the **node's** namespace, not in a container that
disappears with the scenario, so `Cleanup` removes them explicitly. Names stay
under 15 chars (`IFNAMSIZ`) because the subinterface appends `.<tag>`.

What this still does not cover: a full data-plane path (a Gateway VIP whose
egress lands on a tenant VLAN, curled end to end). ARP reachability plus
cross-VLAN silence is the honest minimum for "per-tenant next hop".

## Prerequisite: the legacy SPK network model

**BNK 2.4 ships two mutually exclusive network models**, selected by the CNE
controller's `USE_GATEWAY_SETTINGS` env var. Only one of them reconciles
`F5SPKVlan`:

| | `USE_GATEWAY_SETTINGS=true` | `=false` |
|---|---|---|
| `Infra.spec.networks/vrfs` | programs TMM | quiet |
| `F5SPKVlan` / `Vrf` | **accepted, never reconciled** — no `.status`, no gRPC push, TMM never told | `READY=True`, `declTmm.vlan` + `declTmm.self_ip` |
| `.vlans` in a template | **empty** | **populated** |
| address-less Gateway (`fic-dynamic-ip`) | works | unavailable |

So this scenario needs `bnk.tmm_dataplane_mode: selfip-dag`, which renders
`USE_GATEWAY_SETTINGS=false` (`cne_vars` in `kit/platform/lib.sh`). `Apply` refuses
up front in any other mode, and if `.vlans` comes back empty `Verify` names
this as the cause rather than reporting six mysterious field failures.

This was verified by flipping the var on one cluster, one binary
(f5ingress `v14.91.12-0.4.7`): the same `F5SPKVlan` sat status-less for 109
minutes, then reconciled seconds after the controller restarted with `false`.

## Gotchas this scenario encodes

- **The renderer is plain `text/template` with no Sprig.** Using `list`,
  `dict`, `default` … fails the *whole* render with
  `function "list" not defined` and leaves TMM unconfigured. The template
  ranges over `.vlans` instead of indexing by name (which also avoids the
  hyphen-in-key problem: `.vlans.tenant-red` is a parse error).
- **`GlobalRoutingConfig` is a cluster singleton** — *"Only one
  GlobalRoutingConfig instance is allowed"*. `Apply` deletes a stale one from
  a previous run first.
- **`RoutingTemplate` and `GlobalRoutingConfig` are both immutable**
  (*"updates are not allowed. Please delete and recreate"*).
- **`kind: Vrf`**, not `VRF` — the latter fails with "no matches for kind".
- The `F5SPKVlan` IPv6 pattern rejects non-hex labels, so the self-IPs are
  plain `2001:db8:1::N` (a `2001:db8:red::11` is refused by the CRD schema).
- `spec.bgp` requires `routerIdSource` (or `routerIdSubnet`+`routerIdPrefix`);
  `%POD_IP%` arrives in the context **already expanded** per TMM.

## How to run

Needs BNK 2.4's legacy SPK network model (`bnk.tmm_dataplane_mode: selfip-dag`,
i.e. `USE_GATEWAY_SETTINGS=false`); on any other cluster the scenario reports
itself not applicable.

```bash
ocibnkctl scenario run routing-template-multitenant --poc <pocdir>
```

Or by hand, on any cluster `kubectl` points at (plain `sh` + `kubectl`;
contract: [kit/lib/common.sh](../../lib/common.sh)):

```sh
sh kit/scenarios/routing-template-multitenant/apply.sh
sh kit/scenarios/routing-template-multitenant/verify.sh     # one JSON line per check
sh kit/scenarios/routing-template-multitenant/cleanup.sh
```

The ARP next-hop and isolation probes run from a peer in the TMM node's
network namespace, which needs the nodes to be containers on this host
(`EDGE_DRIVER=ocibnkctl`); elsewhere they are reported skipped.
