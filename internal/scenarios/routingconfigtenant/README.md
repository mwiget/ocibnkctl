# `routing-config-tenant` — per-tenant routing (`RoutingConfig`)

F5 source: [Configure global BGP routing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/configure-global-BGP-routing.html)
&nbsp;·&nbsp; Rating: 🟡 (all assertions pass, but only in `selfip-dag` mode — so it is excluded from `scenario run --all`)
&nbsp;·&nbsp; Depends on: `routing-template-multitenant`

`RoutingConfig` is the VRF-scoped half of BNK 2.4 routing. Where a
`GlobalRoutingConfig` renders one config per TMM **device**, a `RoutingConfig`
renders one per device **per tenant**, into
`/tmm-routing-config/conf-create/tenant/`.

Its template context gains two keys the global one never has:

| key | global scope | tenant scope |
|---|---|---|
| `.tenant.spec.vrf` | absent | `red` |
| `.routeDomainID` | `0` | `24195` (that tenant's route domain) |
| `.vlans` | populated | populated |

Live tenant render:

```
! OCIBNKCTL-TENANT-CTX map[... routeDomainID:24195 ...
    tenant:map[spec:map[templateRef:map[name:scn-rt-tenant] vrf:red]]
    vlans:map[tenant-red:map[selfIPv4:198.51.100.11 tag:2001 vrf:red] ...]]
!
ip vrf red
exit
```

## This explains the design doc's `<no value>`

F5's design doc for the `.vlans` feature shows a template using
`ip vrf {{ .tenant.spec.vrf }}` and a rendered result of:

```
ip vrf <no value>
```

That isn't a bug in the feature — the example is applied as a **global**
template, and `.tenant` only exists in tenant scope. In a `RoutingConfig`
template the same expression renders `ip vrf red`, which is assertion 7 here.

## Known defect — under the *other* network model

With `USE_GATEWAY_SETTINGS=true` (Infra/GatewaySettings) and a VRF programmed
through `Infra.spec.vrfs`, the same CR segfaults the controller:

```
panic: runtime error: invalid memory address or nil pointer dereference
  [signal SIGSEGV code=0x1 addr=0x30]
  internal/k8s.(*LoadBalancerController).RoutingConfigReconciler
      /builds/f5ingress/f5ingress/internal/k8s/controller_routingconfig.go:562
  ← syncRoutingConfig (controller_routingconfig.go:178)
  ← sync (controller.go:4503) ← taskQueue.processItems (priority_queue.go:275)
```

and the blast radius is the cluster, not the CR: `f5-cne-controller`
crash-loops, `f5-validation-svc` goes down with it, and **every** F5 CR apply
then fails admission with `502 Bad Gateway`.

This scenario runs in the legacy SPK model, where the same CR is handled
cleanly — so it does not reproduce the panic, but assertion 2 checks the
controller is still `Running` afterwards, which would catch a regression that
brought the defect into this path. The defect itself is written up in
`docs/bnk-2.4.0-doc-errata.md`.

## Other findings worth knowing

- **`RoutingConfig` is only reconciled in the BNK namespace** (`default`). The
  same CR in another namespace draws no validation webhook, no status and no
  log line at all — "tenant" here means VRF / route domain, **not** Kubernetes
  namespace.
- The status settles through `"CR config deployment is in progress"` before
  reaching `Programmed=True`, so a single read right after apply sees `False`.
- `RoutingTemplate` is immutable — re-running means delete + recreate.
