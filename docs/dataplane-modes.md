# Dataplane modes: how ocibnkctl attaches TMM to the network

`ocibnkctl` deploys the **real, stock BIG-IP Next for Kubernetes (BNK)
2.3.1** binary in **demo mode** (virtio in the pod netns, no DPU/SR-IOV).
On top of that it offers three ways to present TMM's data plane, selected
by `bnk.tmm_dataplane_mode`:

| `tmm_dataplane_mode` | net1 / `mapres` | L3 identity | Reachability | Reference model |
|---|---|---|---|---|
| `standby` (default) | none / `TRUE` | — (one active, rest standby) | stock BNK HA | stock BNK, single active TMM |
| `selfip-dag` | `f5-tmm-dag` NAD / `TRUE` | **self-IP** (`F5SPKVlan`, one per TMM) | self-IP segment + **DAG** | **production BNK** (shared VLAN + DAG) |
| `anycast-bgp` | `bnk-bgp` NAD / `FALSE` | **pod IP** (`net1`) | **BGP** `/32` + **ECMP** | the [`tmmlite`](https://github.com/mwiget/tmmlitectl) model (pod-IP-as-self-IP) |

The interesting part is that **`ocibnkctl` runs both models in one stock
binary**: `selfip-dag` is production BNK's shared-VLAN + self-IP + DAG
shape, and `anycast-bgp` brings the pod-IP + BGP + ECMP model (proven out
in the sibling `tmmlite` project) into the full-BNK path. This note
explains how each wires TMM, why `anycast-bgp` needs **no `F5SPKVlan`**,
how the two all-active modes scale to one TMM per node, and the honest
single-host limits of the demo shape.

This is a design/architecture note. Runtime details live in
[`README.md` → "All-active data plane"](../README.md) and
[`README.md` → "Network topology"](../README.md); the production-BNK side
is sourced from F5's public docs (links at the bottom). The framing and
the comparison tables are adapted from `tmmlite`'s
[`docs/networking-vs-bnk.md`](https://github.com/mwiget/tmmlitectl/blob/main/docs/networking-vs-bnk.md).

---

## 1. How production stock BNK wires TMM (`F5SPKVlan` + self-IP + DAG)

Production BNK is built for a **DPU / SmartNIC** dataplane (NVIDIA
BlueField-3) on an **OVN-Kubernetes** cluster. TMM doesn't ride the
ordinary pod CNI; it bonds and owns *physical* interfaces, declared with
the **`F5SPKVlan`** custom resource — which, per F5's CRD docs,
"configures Self IP addresses for direct connection to the physical
network" plus VLAN tags, MTU, bonding, and DAG packet-hashing.

The defining field is **`selfip_v4s` — a *list* of self-IPs, one per TMM
replica**:

> "The first self IP address in the list is applied to the first TMM Pod,
> the second IP address to the second TMM Pod … ensure sufficient
> addresses based on the number of TMM instances, so each TMM replica is
> assigned a unique Self IP address."

```yaml
# production BNK: F5SPKVlan (abridged)
selfip_v4s:
  - "192.168.10.100"   # → TMM pod 0  (device DP-0)
  - "192.168.10.101"   # → TMM pod 1  (device DP-1)
tag: 3805
bonded: true
cmp_hash: ...          # DAG hashing across replicas
pod_hash: ...
```

The **self-IP is TMM's L3 identity** on each VLAN: where VIPs are hosted
and ARP/ND is answered, the SNAT source on that segment, and the
per-replica address the **DAG** hashes flows across in an active/active
set. That's why the self-IP count is coupled to the replica count — the
operator pre-allocates and tracks a self-IP **per TMM**.

In short: **production BNK gives TMM an explicit L3 presence on real
VLANs, one self-IP per replica, and distributes flows across those
self-IPs with an internal DAG.** `ocibnkctl`'s `selfip-dag` mode is the
faithful demo-shape emulation of this (a software bridge NAD standing in
for the physical VLAN — see §3).

---

## 2. The demo datapath: two wires and one splice

Every `ocibnkctl` TMM pod has the same plumbing — two *wires* and one
*splice*:

- **`net1`** — a Multus bridge-CNI NAD (a software Linux bridge on the
  k3s node). The **client / ingress wire**. In `selfip-dag` it's the
  `f5-tmm-dag` bridge (`192.0.2.0/24`); in `anycast-bgp` it's the
  `bnk-bgp` bridge (`192.168.99.0/24`) and also carries the BGP session.
- **`eth0`** — the **Calico** pod network. The **backend / egress wire**;
  TMM SNATs (`snat_automap`) to its `eth0` pod IP.
- **`mapres`** (`TMM_MAPRES_ADDL_VETHS_ON_DP`) — *not a third path*. When
  `TRUE`, at startup it splices a kernel netdev through a veth pair
  (`xnet1`/`xeth0`) so frames are delivered up into the **TMM userspace
  (DPDK) dataplane** instead of being routed by the kernel. mapres is
  *vertical* (kernel ⇄ userspace); `net1`/`eth0` are *horizontal* (pod ⇄
  pod). The `169.254.0.x` (`tmm`, `tmm-shared`, `tmm-bcast`) addresses
  are TMM-internal and never on the client or server path.

The **mapres state is the hinge between the two all-active modes**:

- `selfip-dag` keeps mapres **`TRUE`** → mapres grabs `net1` onto the
  dataplane as interface **`1.1`** (flushing its kernel IP), and the
  `F5SPKVlan` binds `1.1` and assigns it a self-IP. Ingress VIP traffic
  rides TMM's userspace dataplane, exactly like production.
- `anycast-bgp` sets mapres **`FALSE`** → `net1` stays an ordinary kernel
  interface **keeping its NAD-assigned IP**, so TMM's ZeBOS routing
  daemon (`f5-tmm-routing`) can `update-source net1` for its BGP session.
  In the single-host demo, the advertised VIP `/32`s are installed by the
  peer as **kernel routes**, so data-plane curls reach the Gateways over
  the NAD bridge — a faithful demonstration of the **BGP control plane
  and anycast model**, with the data path kernel-routed rather than
  forced through TMM's DPDK userspace (which in production rides a real
  mlx5 SF/VF, not a veth). See [`README.md` → "Network topology"](../README.md).

---

## 3. The three modes, side by side

| Aspect | `standby` | `selfip-dag` | `anycast-bgp` |
|---|---|---|---|
| Multus NAD on `net1` | none | `f5-tmm-dag` (`192.0.2.0/24`) | `bnk-bgp` (`192.168.99.0/24`) |
| `TMM_MAPRES_ADDL_VETHS_ON_DP` | `TRUE` | `TRUE` (net1 → iface `1.1`) | `FALSE` (net1 keeps kernel IP) |
| TMM L3 identity | — | **self-IP** via `F5SPKVlan` (`192.0.2.10`, `.11`, …) | **pod IP** on `net1` (CNI-assigned) |
| Declares networking via | nothing | `F5SPKVlan` (interface `1.1`, `selfip_v4s`, `pod_hash`) | a Multus NAD + cluster-wide ZeBOS template |
| VIP reachability | stock BNK HA | self-IP segment + **DAG** (`pod_hash=SRC_ADDR`) | **BGP** `/32` advertisement (ZeBOS), per-pod next-hop |
| Load distribution | one active | internal **DAG** across self-IPs | **BGP ECMP** across per-pod `/32` next-hops |
| Active/standby | one active, rest standby | every listed self-IP active | every pod active (the ECMP set is the membership) |
| Upstream router needed | no | **no** | yes (an FRR peer; `ocibnkctl` runs one per TMM node) |
| Add a TMM | bump `tmm_nodes` | bump `tmm_nodes` → one more self-IP rendered | bump `tmm_nodes` → pod self-addresses + self-advertises |

`standby` is BNK's stock HA shape. `selfip-dag` is the production
shared-VLAN + DAG model. `anycast-bgp` is the pod-IP + BGP + ECMP model.

### Datapath — `selfip-dag` (mapres TRUE, self-IP + DAG)

```
                           TMM pod (default)  app=f5-tmm
                          ┌────────────────────────────────────────┐
net1 / f5-tmm-dag bridge  │ net1 ─▶[xnet1]  mapres splice          │ eth0 / Calico
(192.0.2.0/24)            │   becomes interface "1.1"              │ (backend wire)
  ── ingress ──▶          │   F5SPKVlan binds 1.1 → self-IP        │ ── SNAT-automap ──▶ backends
                          │   192.0.2.1{0,1,…} (one per TMM)       │ src = TMM eth0 pod IP
                          │   DAG: pod_hash=SRC_ADDR               │
                          └────────────────────────────────────────┘
   one self-IP per TMM, all on the shared f5-tmm-dag bridge; the DAG
   hashes flows across the replicas' self-IPs (production BNK's model).
```

### Datapath — `anycast-bgp` (mapres FALSE, pod IP + BGP)

```
 FRR peer (per TMM node)            TMM pod (default)  app=f5-tmm
 ┌──────────────────┐              ┌────────────────────────────────────────┐
 │ bnk-frr DaemonSet│ net1/bridge  │ net1 192.168.99.x  (kernel IP kept)    │ eth0 / Calico
 │ net1 192.168.99.2│ ◀── BGP ──▶  │  ZeBOS (f5-tmm-routing):               │ ── SNAT-automap ──▶
 │ AS 65001         │  br-bnk-bgp  │    router bgp 65000                    │ backends
 │ listen-range     │              │    bgp router-id %%POD_IP%%  ← per pod │
 │ 192.168.99.0/24  │ ◀ VIP /32    │    redistribute kernel + connected     │
 └──────────────────┘  advertised  │    neighbor 192.168.99.2               │
                                   │      update-source net1                │
                                   └────────────────────────────────────────┘
                       installs VIP /32 as a kernel route via net1 → data-plane
                       curls reach Gateways over the NAD bridge (BGP demo).

   each TMM advertises the SAME VIP /32 from its OWN session; the router
   learns it from N next-hops and ECMPs across the TMM pods (anycast).
```

---

## 4. Why `anycast-bgp` needs no `F5SPKVlan` — the pod IP *is* the self-IP

A BNK self-IP does three jobs. `anycast-bgp` serves all three **without**
one, because the CNI already gave the pod its addresses and BGP handles
reachability:

| What a production self-IP provides | How `anycast-bgp` provides it without one |
|---|---|
| **L3 identity** of TMM on a VLAN (ARP/ND, the address TMM "is") | the **CNI-assigned pod IPs** — `net1` (ingress/BGP) and `eth0` (egress). The pod *is* its address; there's no separate VLAN to own one on. |
| **VIP reachability** (VIP on the self-IP's segment, drawn in by ARP/L2) | **BGP routing**: ZeBOS advertises the VIP `/32`, next-hop `net1`. Reachability is a *route*, not an L2 self-IP. |
| **SNAT source** on the segment | `snat_automap` → the `eth0` Calico pod IP. |
| **Per-replica identity** for the DAG (self-IP-per-TMM) | not needed — BGP/ECMP replaces the DAG (see §5). |

So **`F5SPKVlan` exists to attach TMM to *physical* VLANs and give it
self-IPs there.** `anycast-bgp` removes the VLAN attachment entirely: the
CNI is the L2/L3 provider and BGP is the reachability mechanism, leaving
nothing for an `F5SPKVlan` to declare. "**pod IP replaces self-IP**" is
literal — the CNI-allocated `net1` address is TMM's effective self-IP,
assigned automatically with no self-IP pool, VLAN tag, or per-replica
list to manage. (`selfip-dag`, by contrast, *does* use an `F5SPKVlan` —
it's the faithful production emulation.)

---

## 5. Multi-node: one TMM per node

This is where the two all-active modes diverge most.

### `selfip-dag` — shared bridge + self-IP-per-replica + internal DAG

Scaling raises the TMM count **and** extends the self-IP list so each new
pod gets a unique self-IP (`ocibnkctl` renders `192.0.2.10`, `.11`, … —
one per TMM, indexed to device DP-0, DP-1, …). All TMMs share the **same
`f5-tmm-dag` bridge**; the **DAG** (`pod_hash=SRC_ADDR`) distributes flows
across the replicas' self-IPs. This mirrors production: "one TMM cluster
on a shared VLAN," with flow affinity managed *inside* TMM.

### `anycast-bgp` — independent per-node TMMs + anycast VIP + ECMP

`ocibnkctl scale` joins a node, labels it `app=f5-tmm`, and lands a TMM on
each. There's **no self-IP list to extend**:

- Each TMM gets its **own** `net1` from the per-node Multus host-local
  IPAM — automatically, zero pre-allocation.
- Each TMM independently advertises the **same VIP `/32`** over its
  **own** BGP session (next-hop = that pod's `net1`). The upstream router
  (an FRR peer) learns the VIP from **N next-hops** and **ECMP**s across
  the TMM pods — anycast.
- A single cluster-wide ZeBOS ConfigMap suffices because
  `bgp router-id %%POD_IP%%` is a **per-pod token FLO expands**, so each
  pod gets a distinct router-id from one template.

### Comparison

| At N TMMs (one per node) | `selfip-dag` | `anycast-bgp` |
|---|---|---|
| Per-TMM address | **self-IP per replica**, rendered into `F5SPKVlan` | **pod IP per pod**, CNI-assigned automatically |
| Address bookkeeping | **O(N)** self-IPs (capped at `MaxTMMNodes`) | **O(0)** — CNI + BGP do it |
| L2/L3 model | shared `f5-tmm-dag` bridge, all replicas on it | independent per-node pods; no shared VLAN |
| Load distribution | internal **DAG** (`pod_hash`) across self-IPs | **BGP ECMP** across per-pod `/32` next-hops |
| Active/standby | every listed self-IP active | every pod active; the ECMP set is the membership |
| Add a TMM | bump `tmm_nodes`; one more self-IP rendered | `scale` adds a node+pod; it self-addresses + self-advertises |

`anycast-bgp` is **simpler and self-scaling** (no per-replica self-IP
state, each pod self-contained), at the cost of what the DAG over a shared
VLAN buys: there's **no cross-TMM flow-state sharing / connection
mirroring**, and an ECMP rehash on a topology change can reset flows that
move between pods. `selfip-dag` gives tighter active/active flow
distribution; `anycast-bgp` trades that for zero self-IP management and
lets the routing fabric own the load-spreading.

---

## 6. Honest single-host limits (both all-active modes)

The whole `ocibnkctl` cluster is **k3s nodes as containers on one host**,
so neither all-active mode fans one VIP's *throughput* across nodes:

- **`selfip-dag`** — each TMM serves only the traffic that physically
  lands on its own node's `f5-tmm-dag` bridge. The per-node bridges are
  isolated, so a single VIP's traffic is **not** spread across nodes.
- **`anycast-bgp`** — the per-node `bnk-bgp` bridges are likewise isolated
  L2 segments (host-local IPAM even hands out the same `192.168.99.x`
  range per node), so `ocibnkctl` runs **one FRR peer per `app=f5-tmm`
  node** (pinned to a static `192.168.99.2`) and each TMM advertises its
  VIP `/32` to its **node-local** peer. The demo validates the **anycast
  model** — every TMM forms its own session and advertises the same
  `/32`, with a **distinct per-pod router-id** — but each FRR sees only
  the one TMM on its node (**one next-hop, not N**), so true cross-node
  ECMP fan-out is **not** demonstrable on a single host.

Real cross-node fan-out (hardware DAG, or an upstream ToR ECMP-ing across
N next-hops) needs either DPUs/SR-IOV (`selfip-dag`'s production target)
or a **shared-L2 underlay + an upstream ToR receiving all N sessions**
(`anycast-bgp`'s production target) — a multi-host deployment, out of
scope for the single-host demo. Adding TMM nodes scales **availability**
and **per-node capacity** (steer different clients at different nodes),
not one VIP's throughput.

The `bgp-anycast` scenario asserts exactly this shape: every Running TMM
reaches BGP `Established`, each has a distinct ZeBOS router-id (the
`%%POD_IP%%`-per-pod proof), and each FRR sees its node-local TMM count —
documenting the ECMP gap rather than failing on it (hence its **amber**
rating).

---

## Sources

- F5SPKVlan CRD — [clouddocs.f5.com/.../custom-resource-definitions/spk-vlan-crd.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-vlan-crd.html)
- Configure the Network — [clouddocs.f5.com/.../bnk-configure-network.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.0.0-GA/bnk-configure-network.html)
- BIG-IP Next for Kubernetes CRDs index — [clouddocs.f5.com/.../spk-custom-resources.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/spk-custom-resources.html)
- SPK troubleshooting (mlx5/vfio/DPDK PMD) — [clouddocs.f5.com/.../spk-troubleshooting.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.0.0-GA/spk-troubleshooting.html)
- ocibnkctl side: [`README.md`](../README.md) ("All-active data plane" + "Network topology"); `internal/deploy/activeactive.go` (selfip-dag), `internal/deploy/bgp.go` (anycast-bgp), `internal/poc/schema.go` (`tmm_dataplane_mode`).
- Adapted from the sibling [`tmmlite`](https://github.com/mwiget/tmmlitectl) project's [`docs/networking-vs-bnk.md`](https://github.com/mwiget/tmmlitectl/blob/main/docs/networking-vs-bnk.md) and its README "Datapath: two wires and one splice".
