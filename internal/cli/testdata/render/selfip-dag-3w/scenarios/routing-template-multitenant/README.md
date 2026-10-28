# routing-template-multitenant

Expose VLAN/Self-IPs in a RoutingTemplate (.vlans) — two tagged tenants on one interface

- Rating: **amber**

## What it does

Exercises the F5 design "Expose VLAN/Self-IPs in Routing template": VLAN
self-IPs are allocated per TMM by the controller, and a new top-level
.vlans template variable makes each TMM's own self-IP addressable from a
RoutingTemplate — following the existing .global.tmmAllocatedIPs pattern.

Multi-tenant shape: two tagged VLANs on the SAME TMM interface (1.1),
tenant-red (tag 2001, vrf red) and tenant-green (tag 2002, vrf green),
each with one self-IP per TMM. One RoutingTemplate then renders both
tenants' routing config from .vlans, and the controller renders it once
per TMM device with that device's own addresses.

Asserts: both Vrf CRs and both F5SPKVlans reach Programmed; TMM binds both
VLANs to interface 1.1 with no declTmm error; every TMM's rendered config
carries all six documented .vlans fields for both tenants; each TMM renders
ITS OWN self-IP (element order of selfip_v4s maps to DP-0, DP-1, …); and the
two tenants stay distinct (red never renders green's address or vrf).

REQUIRES the legacy SPK network model — bnk.tmm_dataplane_mode: selfip-dag,
which renders USE_GATEWAY_SETTINGS=false. BNK 2.4 ships two mutually
exclusive network models and only that one reconciles F5SPKVlan; under
Infra/GatewaySettings the CRs are accepted and silently ignored, leaving
.vlans empty. Verify detects that and reports it as the cause rather than
as a failed assertion.

Cleanup deletes the GlobalRoutingConfig, RoutingTemplate, both VLANs and
both Vrfs. Note GlobalRoutingConfig is a CLUSTER SINGLETON ("Only one
GlobalRoutingConfig instance is allowed"), so Apply removes a stale one
left behind by an earlier run.

## Manifests

- `manifests/01-vrfs.yaml`
- `manifests/02-vlans.yaml`
- `manifests/03-routing-template.yaml`
- `manifests/04-global-routing-config.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
