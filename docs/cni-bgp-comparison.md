# Kubernetes CNI popularity & BGP support

A survey of the most-deployed Kubernetes CNIs, ordered by **installed
production base** (i.e. weighted by distro / managed-cloud *defaults*, not by
survey self-selection), with each CNI's BGP capability and whether that BGP is
used for underlay, overlay, or both.

## Why "installed base" differs from survey rankings

Popularity surveys (CNCF-style) measure **self-reported CNI choice** and put
Cilium / Calico / Flannel on top. That undercounts the CNIs baked into
managed platforms — OpenShift operators say "we run OpenShift," not "we run
OVN-Kubernetes," and EKS/AKS/GKE users rarely report the provider default as
their CNI. Ranking by clusters *actually running in production* therefore puts
the cloud and distro defaults first.

A second pattern falls out of this: **the CNIs with the largest installed base
mostly don't speak BGP**, because cloud defaults delegate routing to the
provider's own underlay fabric (AWS VPC, Azure VNet, GCP Alias IPs). BGP
matters precisely where those cloud CNIs don't reach — on-prem, bare-metal, and
telco — which is Calico / Cilium / kube-router / Kube-OVN territory (and
relevant to this repo's F5 BNK + FRR/OcNOS BGP work).

> When BGP is present it is almost always used the same way: to advertise
> Pod / Service routes into the **underlay** fabric so external routers reach
> Pods natively (no encap). The CNI's own *datapath* may still be an overlay.

## Comparison table

| #  | CNI | Distro / platform that defaults to it | Install base driver | BGP | Underlay / Overlay / Both |
|----|-----|----------------------------------------|---------------------|-----|---------------------------|
| 1  | **AWS VPC CNI** | Amazon EKS | Default on every EKS cluster | ❌ No (native VPC routing) | Underlay-native (real VPC IPs) |
| 2  | **OVN-Kubernetes** | OpenShift 4.11+ (OKD, Azure Red Hat OpenShift) | OpenShift's only new-install option | ⚠️ Emerging (released in OVN-K 1.1.0 / OpenShift 4.20+, FRR-based, EVPN pending) — see [below](#ovn-kubernetes-bgp-emerging) | Overlay (Geneve); underlay advertisement via BGP |
| 3  | **Cilium** | GKE Dataplane V2 (default for new/Autopilot), AKS "Azure CNI Powered by Cilium", many distros | Cloud defaults + self-managed | ✅ Yes (BGP Control Plane, GoBGP) | Both |
| 4  | **Calico** | Kubespray, MicroK8s, GKE NetworkPolicy, EKS/AKS add-on, Tanzu (option) | DIY / on-prem default of choice | ✅ Yes (native, mature) | Both |
| 5  | **Azure CNI** (azure-vnet) | AKS (classic / non-Cilium path) | AKS default | ❌ No (native VNet routing) | Underlay-native, or Overlay mode |
| 6  | **Flannel** | k3s, Talos Linux, k3d, most kubeadm tutorials | Edge / IoT / dev / CI ubiquity | ❌ No | Overlay (VXLAN), `host-gw` |
| 7  | **Canal** (Flannel+Calico) | Rancher RKE1 | Historical Rancher default | ❌ No (datapath) | Overlay (Calico only for policy) |
| 8  | **Antrea** | vSphere with Tanzu / TKG, TKGI | VMware enterprise base | ✅ Yes (`BGPPolicy`) | Overlay (Geneve); underlay advertisement |
| 9  | **Kube-OVN** | No mainstream distro default (KubeSphere option; telco / China clouds) | Chosen, not defaulted | ✅ Yes (BGP speaker) | Both |
| 10 | **Weave Net** | None current (common in legacy kubeadm guides) | Declining / unmaintained | ❌ No | Overlay (mesh) |
| 11 | **kube-router** | k0s | Lightweight / bare-metal | ✅ Yes (native, GoBGP) | Both (BGP direct routing default; optional overlay tunnel) |

## Notes

- **"Default" ≠ "only."** AKS and GKE let you swap the dataplane (hence Cilium
  and Azure CNI both appear for AKS); EKS lets you replace VPC CNI with
  Cilium/Calico in chaining mode.
- **kube-router** (#11) is the k0s default and a full BGP/GoBGP underlay CNI —
  worth knowing for bare-metal / BGP environments even though it's well down the
  install-base ranking.
- The exact ordering of #1–#5 depends on how you weight EKS vs OpenShift vs GKE
  cluster counts, which no vendor publishes cleanly. Treat the ordering as
  directional; the top tier (VPC CNI, OVN-K, Cilium, Calico) is solid.

## OVN-Kubernetes BGP (emerging)

OVN-K historically had **no routing-protocol integration** — pure Geneve
overlay east/west, with external reachability bolted on via third-party
operators. The BGP work (design proposal **OKEP-5296**) changes that: it ships
in **OVN-Kubernetes 1.1.0** as a foundational feature and surfaces in
**OpenShift 4.20+** (and OKD). It is new and FRR-only; EVPN/L3VPN are still
future work — hence the ⚠️ rating, but it is now a *real* underlay-advertisement
option rather than a proposal.

Unlike most CNIs that only advertise outward, it does both directions:

- **Route advertisement** — expose Pod subnets, Egress IPs, and
  user-defined-network (UDN) subnets to the provider network via BGP, so
  external routers reach Pods directly (no encap, no NAT, no LoadBalancer hop).
- **Route import** — externally learned BGP routes dynamically program the OVN
  logical routers.

### How it's implemented

It does not ship a new BGP speaker — it reuses the CNCF stack:

- **FRR (FRRouting)** is the actual BGP daemon (peering, advertise/import, BFD).
- **frr-k8s** (MetalLB's Kubernetes API for FRR) is the management layer.
  OVN-K and the **MetalLB Operator share the same frr-k8s deployment**, so
  LoadBalancer-IP advertisement (MetalLB) and Pod/Egress/UDN advertisement
  (OVN-K) coexist on one FRR.
- OVN-K watches netlink, configures the logical routers, and writes
  `FRRConfiguration` objects to drive FRR.

### The API — `RouteAdvertisements` CRD

The operator-facing knob. It selects **what** to advertise (Pod subnets, Egress
IPs, UDN subnets), **which nodes** advertise it, **which `FRRConfiguration`** to
attach to, and the **target VRF** (`default` for route leaking, `auto` for the
network's native VRF / VRF-Lite). When OVN-K sees a `RouteAdvertisements` CR it
generates the matching `FRRConfiguration` objects automatically.

### Enabling it (OpenShift 4.20+)

```sh
oc patch Network.operator.openshift.io cluster --type=merge -p='{"spec":{
  "additionalRoutingCapabilities": {"providers": ["FRR"]},
  "defaultNetwork":{"ovnKubernetesConfig":{"routeAdvertisements":"Enabled"}}}}'
```

Then: create an `FRRConfiguration` for the BGP peering → optionally carve out
primary UDNs (`ClusterUserDefinedNetwork` + namespaces labeled
`k8s.ovn.org/primary-user-defined-network`) → create a `RouteAdvertisements` CR.

### Supported modes / limits

- **L3 topology**: per-node subnet advertisement (direct routing).
  **L2 topology**: cluster supernet advertisement.
- **VRF-Lite**: full in local-gateway mode; limited in shared-gateway mode.
- Works alongside Egress Service / Firewall / QoS, NetworkPolicy / ANP, and
  direct Pod ingress on UDNs. UDN isolation is preserved via subnet
  filtering / ACLs.
- **Non-goals**: no per-VRF BGPd, no automatic inter-cluster L3VPN, **FRR only**
  (no other speakers), no OSPF. **EVPN is planned but not yet shipped.**

Architecturally this is close to the FRR + OcNOS BGP underlay this repo's
scenarios build (see [BGP / NAD detail in CLAUDE.md](../CLAUDE.md)), just
managed through OVN-K's CRDs instead of raw FRR config.

## Two rankings, opposite stories

| Lens | Leaders |
|------|---------|
| By installed base (this table) | Cloud-native, mostly non-BGP CNIs (VPC CNI, OVN-K, Cilium, Calico) |
| By "actively chosen for on-prem / bare-metal" | BGP-capable CNIs (Calico, Cilium, kube-router, Kube-OVN) |

## Sources

- [Cloud provider CNI defaults — DEV: Flannel vs Cilium vs Calico + Cloud Provider CNIs](https://dev.to/pendelabhargavasai/kubernetes-cni-complete-guide-flannel-vs-cilium-vs-calico-cloud-provider-cnis-5c6c)
- [GKE Dataplane V2 default / Azure CNI Powered by Cilium — Microsoft Learn](https://learn.microsoft.com/en-us/azure/aks/azure-cni-powered-by-cilium)
- [Cilium BGP Control Plane documentation](https://docs.cilium.io/en/stable/network/bgp-control-plane/bgp-control-plane-configuration/)
- [OVN-Kubernetes default since OpenShift 4.11 — Red Hat docs](https://docs.redhat.com/en/documentation/openshift_container_platform/4.16/html/networking/ovn-kubernetes-network-plugin)
- [BGP — OVN-Kubernetes (OKEP-5296)](https://ovn-kubernetes.io/okeps/okep-5296-bgp/)
- [Exposing OpenShift networks using BGP — Red Hat Developer (Oct 2025)](https://developers.redhat.com/articles/2025/10/23/exposing-openshift-networks-using-bgp)
- [Selective network hosting with BGP router in OpenShift — Red Hat Developer (Jan 2026)](https://developers.redhat.com/articles/2026/01/21/selective-network-hosting-bgp-router-openshift)
- [About route advertisements — OKD docs](https://docs.okd.io/latest/networking/advanced_networking/route_advertisements/about-route-advertisements.html)
- [BGP Support — Kube-OVN docs](https://kubeovn.github.io/docs/v1.13.x/en/advance/with-bgp/)
- [Antrea default for vSphere with Tanzu — CormacHogan.com](https://cormachogan.com/2020/11/16/a-closer-look-at-antrea-the-new-cni-for-vsphere-with-tanzu-guest-clusters/)
- [Canal is the Rancher RKE default — Rancher docs](https://ranchermanager.docs.rancher.com/faq/container-network-interface-providers)
- [kube-router — GitHub](https://github.com/cloudnativelabs/kube-router)
