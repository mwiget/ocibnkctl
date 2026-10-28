# fic-dynamic-ip

Dynamic IP address allocation (FIC for Gateway API) — address-less Gateway + GatewaySettings/Infra IPAM

- Rating: **green**
- Depends on: `bgp-peer-frr` (apply those first)

## What it does

Demonstrates dynamic IP allocation for a Gateway API Gateway.

The Gateway is applied WITHOUT spec.addresses. A GatewaySettings CR in the
same namespace references the platform IPAM pool (Infra default/infra,
ipams bnk-dynamic-vips = 203.0.113.110-119 — applied by ocibnkctl deploy cne
together with USE_GATEWAY_SETTINGS=true on the CNE controller) and the
Gateway binds to it through spec.infrastructure.parametersRef
(group gateway.k8s.f5.com, kind GatewaySettings).

Status on BNK 2.4.0 in ocibnkctl's demo-TMM shape (🟢): the controller
allocates an address from the pool, populates gateway.status.addresses,
programs the listener, OcNOS advertises the /32 to the external FRR and
end-to-end curls succeed. On 2.3.x this was amber — the F5BnkGateway pool
was never bridged into IPAM/IPAMRange CRs in the demo deployment, so the
Gateway stalled at AddressNotAssigned.

Asserts: GatewaySettings Accepted+ResolvedRefs=True; Gateway
Programmed=True with status.addresses inside the pool; HTTPRoute
Accepted=True; FRR learned the allocated /32; 5/5 curls via the external
FRR reach nginx through the allocated VIP.

Cleanup deletes the scn-fic-dyn namespace (the platform Infra CR stays).

## Manifests

- `01-namespace.yaml`
- `02-gatewaysettings.yaml`
- `03-backend.yaml`
- `04-gateway.yaml`
- `05-httproute.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
