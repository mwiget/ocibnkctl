# routing-config-tenant

Per-tenant routing (RoutingConfig) — .tenant + per-tenant route domain in the template context

- Rating: **amber**
- Depends on: `routing-template-multitenant` (apply those first)

## What it does

RoutingConfig is the VRF-scoped half of BNK 2.4 routing: it binds a
RoutingTemplate to one tenant (spec.vrf) and renders per TMM device into
/tmm-routing-config/conf-create/tenant/.

Asserts: the RoutingConfig reaches Programmed=True; a tenant config is
rendered on every TMM; and the tenant template context carries what the
global one does not —

  .tenant.spec.vrf   the tenant's VRF name (red)
  .routeDomainID     that tenant's route domain, non-zero
  .vlans             still populated, so a tenant template can source from
                     its own per-TMM VLAN self-IP

Depends on routing-template-multitenant for the Vrf and the tagged per-tenant
F5SPKVlans, and therefore on the same legacy SPK network model
(USE_GATEWAY_SETTINGS=false, i.e. bnk.tmm_dataplane_mode: selfip-dag).

KNOWN DEFECT, elsewhere: under the OTHER network model
(USE_GATEWAY_SETTINGS=true, VRFs from Infra.spec.vrfs) the same CR segfaults
the controller —

  panic: runtime error: invalid memory address or nil pointer dereference
    internal/k8s.(*LoadBalancerController).RoutingConfigReconciler
        internal/k8s/controller_routingconfig.go:562

and f5-cne-controller crash-loops, taking f5-validation-svc with it so every
F5 CR apply fails admission with 502. This scenario does not reproduce that —
it runs in the legacy model, where the same CR is handled cleanly. See
docs/bnk-2.4.0-doc-errata.md.

## Manifests

- `01-routing-template.yaml`
- `02-routing-config.yaml`

Not every file is applied unconditionally by `ocibnkctl scenario run`; check the
description above for ordering and waits between steps.
