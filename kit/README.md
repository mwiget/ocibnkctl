# kit — BNK manifests as plain files

Every manifest `ocibnkctl` applies, as files you can read, adapt and
`kubectl apply` yourself — on a cluster `ocibnkctl` built or any other. The
binary embeds this directory (`kit.go`), so it applies exactly these bytes.
This is Phase 1 of [docs/agentic-kit-plan.md](../docs/agentic-kit-plan.md).

For one PoC's ready-to-apply set, in order, with the commands: `ocibnkctl
render`. This directory is the source that render fills in.

## Layout

```
platform/   the BNK install, by phase (cluster → prereqs → flo → shrink → cne)
scenarios/  <scenario-name>/manifests/ — one directory per `ocibnkctl scenario`
lib/        render.sh — fills templates from environment variables
```

## Templates

Files under `platform/` and scenario files ending in `.tmpl` are templates.
They use exactly two constructs:

| Construct | Meaning |
|---|---|
| `${NAME}` | the value of variable `NAME` |
| `#@if NAME` … `#@end` (each on its own line) | keep the lines between only when `NAME` is non-empty; blocks nest |

Every variable a template uses must be set; an empty value is fine, an unset
one is an error. `$NAME` without braces and shell forms like `${NAME:-x}` are
left alone. Fill a template from the shell (POSIX sh + awk, macOS included):

```sh
NAME=f5-operators sh kit/lib/render.sh kit/platform/prereqs/namespace.yaml | kubectl apply -f -
```

The Go renderer (`kit.Render`) follows the same rules; `TestRenderShellParity`
checks the two agree on every template here, under every awk installed.
Scenario files without `.tmpl` are applied verbatim, never rendered.

## platform/

Defaults are what `ocibnkctl` passes for a default PoC.

### cluster/ — applied by `cluster up`

| File | Variables | Why |
|---|---|---|
| `nad-crd.yaml` | — | the Multus `NetworkAttachmentDefinition` CRD, so NADs can be created before Multus is installed |
| `standard-sc.yaml` | — | BNK PVCs ask for `storageClassName: standard`; k3s names its default `local-path`, so this adds `standard` on the same provisioner |

### prereqs/ — `deploy prereqs`

| File | Variables | Why |
|---|---|---|
| `namespace.yaml` | `NAME` (`f5-operators`, `f5-cne-core`) | operator and shared-component namespaces |
| `far-secret.yaml` | `NAMESPACE`, `DOCKERCONFIGJSON_B64` | `far-secret` image-pull Secret for `repo.f5.com`, in `f5-operators`, `f5-cne-core` and `default`. The value is the base64 of a dockerconfigjson whose auth is `_json_key_base64:<base64 SA key>` from the FAR tarball |

### flo/ — `deploy flo`

| File | Variables | Why |
|---|---|---|
| `bnk-ca-issuer.yaml` | — | self-signed root → `bnk-ca` CA certificate → `bnk-ca-cluster-issuer`, the ClusterIssuer FLO and the CNEInstance use for internal certs |
| `flo-values.yaml` | `OPERATOR_NAMESPACE` (`f5-operators`), `SHARED_NAMESPACE` (`f5-cne-core`), `CLUSTER_ISSUER` (`bnk-ca-cluster-issuer`) | helm values for the `f5-lifecycle-operator` chart |

### shrink/ — `deploy shrink` (tight hosts only)

| File | Variables | Why |
|---|---|---|
| `kyverno-shrink-requests.yaml` | `SHARED_NAMESPACE`, `OPERATOR_NAMESPACE`, `CPU_REQUEST` (`25m`), `MEMORY_REQUEST` (`128Mi`) | Kyverno policy capping container *requests* at admission. FLO reasserts every workload spec on its reconcile loop, so admission is the only layer that sticks. Must be admitting before `deploy cne` creates the TMM/DSSM pods. `{{ element.name }}` is Kyverno's own syntax, not a kit variable |

### cne/ — `deploy cne`

| File | Variables | Why |
|---|---|---|
| `nad-bridge-whereabouts.yaml` | `NAD_NAME` (`bnk-bgp`), `NAMESPACE` (`default`), `BRIDGE` (`br-bnk-bgp`), `SUBNET` (`192.168.<edge_octet>.0/24`), `RANGE_START`/`RANGE_END` (`.160`–`.250`) | anycast-bgp: TMM's net1. Whereabouts allocates cluster-wide, so N TMM pods (one per worker) on one shared edge L2 never collide. The subnet must be the L2 the BGP peer sits on |
| `nad-bridge-host-local.yaml` | same names; selfip-dag uses `f5-tmm-dag` / `br-f5-tmm-dag` / `192.0.2.0/24` (`.20`–`.250`) | selfip-dag: per-node bridge with host-local IPAM, grabbed by mapres as interface `1.1` |
| `ocnos-template.yaml` | `CONFIGMAP_NAME`, `NAMESPACE`, `LOCAL_AS` (`65000`), `PEER_IP` (`192.168.<edge_octet>.41`), `PEER_AS` (`65001`), `VIP` (empty) | anycast-bgp: the OcNOS (ZebOS) config every TMM loads. `%%POD_IP%%` is expanded by FLO per pod, giving each TMM a unique router-id. OcNOS only redistributes through a route-map, hence `RMALL`. `#@if VIP` adds a `network <vip>/32` statement; empty relies on `redistribute kernel`. Apply before the CNEInstance so TMM boots with it |
| `cne-instance.yaml` | see below | the `CNEInstance` FLO turns into the whole BNK install. Its comments explain each setting |
| `license-jwks-configmap` | — (built from `jwks-tst.json`) | test JWTs only: TEEM's current test JWKS. CWC's built-in test certs expired, so a test JWT fails registration without it |
| `jwks-tst.json` | — | TEEM's test JWKS; refresh when TEEM rotates keys |
| `license.yaml` | `NAME` (`f5-cne-cluster-license`), `NAMESPACE` (`f5-cne-core`), `OPERATION_MODE` (`connected`), `JWT`, `JWKS_CONFIGMAP` (set for a test JWT: `cwc-jwks-cm`), `JWKS_KEY` (`key`) | the License CR. Apply after the CNEInstance, once FLO has created the `licenses.k8s.f5net.com` CRD. Don't re-apply an Active License: CWC re-registers and fails |
| `gatewayclass.yaml` | `GATEWAYCLASS_NAME` (`bnk-gatewayclass`) | the GatewayClass every BNK Gateway references. Wait for Accepted before creating Gateways |
| `infra.yaml` | `INFRA_NAME` (`infra`), `NAMESPACE`, `VIP_POOL` (`bnk-dynamic-vips`), `VIP_RANGE_START`/`END` (`203.0.113.110`–`.119`) | BNK 2.4 singleton IPAM source for address-less Gateways. Not used in selfip-dag (legacy SPK network model) |
| `f5spkvlan.yaml` | `VLAN_NAME`, `NAMESPACE`, `INTERFACE` (`1.1`), `SELF_IPS` (comma-separated, one per TMM), `PREFIX_LEN` (`24`), `POD_HASH` (`SRC_ADDR`) | selfip-dag only: one self-IP per TMM plus the stateless pod_hash DAG |

`cne-instance.yaml` variables, by what drives them:

| Variable | Default | Set from |
|---|---|---|
| `INSTANCE_NAME` | `bnk-instance` | — |
| `MANIFEST_VERSION` | the pinned BNK release manifest | `internal/version` |
| `DEPLOYMENT_SIZE` | `Small` | `bnk.deployment_size` |
| `NAD_NAME` (`#@if`) | `bnk-bgp` (anycast-bgp), `f5-tmm-dag` (selfip-dag), empty (standby → no `networkAttachments`) | `bnk.tmm_dataplane_mode` |
| `MAPRES_ON_DP` | `FALSE` for anycast-bgp (net1 keeps its kernel IP for BGP), else `TRUE` | `bnk.tmm_dataplane_mode` |
| `USE_GATEWAY_SETTINGS` | `false` for selfip-dag (legacy SPK model), else `true` | `bnk.tmm_dataplane_mode` |
| `METRIC_SUBSYSTEM` | `true`; `false` on `host_profile: small` | `bnk.host_profile` |
| `LOGGING_SUBSYSTEM` | `false`; `true` with AI gateway metrics | `bnk.ai_gateway.ai_metrics` |
| `AIGW_ENABLED` (`#@if`) | empty | `bnk.ai_gateway.enabled` |
| `AIGW_METRICS` | `false` | `bnk.ai_gateway.ai_metrics` |
| `AIGW_CONSOLE_URL` (`#@if`) | empty | `bnk.ai_gateway.console_url` |

## scenarios/

`scenarios/<name>/manifests/` holds the files `ocibnkctl scenario run <name>`
applies, numbered in apply order.

**Script scenarios** carry everything else too, and run by hand on any
cluster `kubectl` points at — no ocibnkctl needed:

```
scenarios/<name>/
  scenario.yaml   name, title, rating, dependencies, description
  scenario.env    the scenario's variables and defaults (environment wins)
  apply.sh        preflight (kit_require), then apply manifests/
  verify.sh       one JSON line per check; exit 0 when all pass
  cleanup.sh      remove what apply.sh created
  README.md
```

```sh
sh kit/scenarios/cluster-wide-watch/apply.sh
sh kit/scenarios/cluster-wide-watch/verify.sh
sh kit/scenarios/cluster-wide-watch/cleanup.sh
```

Data-plane checks need a client outside the cluster that reaches Gateway
VIPs over the routes TMM advertises. `EDGE_DRIVER` (`lib/edge.sh`) says
where it is: `none` (the default by hand: those checks are reported as
skipped), `external` (`EDGE_EXEC` / `EDGE_VTYSH` run commands on your own
client and BGP router), or `ocibnkctl` (the external FRR container
`cluster up` builds; `ocibnkctl scenario run` sets it). The script
helpers are in `lib/common.sh` (assertions, apply, retry, preflight),
`lib/edge.sh` and `lib/bnk.sh` (TMM pods, OcNOS redistribute).

Script scenarios so far: `cwc-admin-access`, `cluster-wide-watch`,
`http-routing-e2e`. The rest still have their checks in Go
(`internal/scenarios/<pkg>/`); what each checks is in `ocibnkctl scenario
list` and in the per-scenario README that `ocibnkctl render` writes. Two files are templates, filled from
live cluster state at apply time: `ai-semantic-cache/manifests/04-gateway.yaml.tmpl`
(`CACHE_IP`) and `external-resource-pool/manifests/05-pool.yaml.tmpl`
(`BACKEND_IP`).
