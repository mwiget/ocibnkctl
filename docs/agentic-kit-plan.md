# Plan: from ctl binary to agentic kit

Status: Phases 0–2 implemented 2026-10-28.

## Why

`ocibnkctl` embeds most of its business logic — FLO values, the CNEInstance,
the License CR, NADs, the OcNOS template, the Kyverno shrink policy, every
scenario's manifests and assertions — inside the Go binary. That works when
the binary drives a cluster it built itself, but it fails three other
audiences:

1. **The manual operator** who wants *just* the FLO values or the CNEInstance
   or a Gateway API CR to read, adapt, and `kubectl apply` by hand.
2. **The learner** who wants to understand *why* each step exists. Today that
   knowledge lives in Go code comments (`internal/cli/deploy.go` alone carries
   the License resume-safe skip, the quota-retry window, the OcNOS pre-seed,
   the TMM re-roll-on-drift).
3. **The third-party cluster owner** who has BNK running on a cluster
   `ocibnkctl` didn't build and wants to run the scenarios against it.

The direction: **manifests and procedures are plain files; the binary is one
optional driver over them.** An agent (driven by `AGENTS.md`) or a human with
`bash`, `kubectl`, `helm` and `jq` must be able to do everything the binary
does with the scenarios and the in-cluster deploy.

## Where the coupling is today

| Area | Form today | Problem |
|---|---|---|
| Platform templates | `internal/embedded/templates/*.tmpl` (Go `text/template`, with `{{if}}`/`{{range}}`) | not usable by hand |
| Inline manifests | `fmt.Sprintf` in `deploy/bgp.go`, `flo.go`, `activeactive.go`, `license.go`, `infra.go`; GatewayClass inline in `cli/deploy.go` | invisible unless you read Go |
| Deploy ordering + rationale | code + comments in `cli/deploy.go` | knowledge trapped in source |
| Scenario manifests | 14/18 scenarios have `manifests/*.yaml` (good) | apply order lives in `Apply()`, checks in `Verify()` |
| Scenario data-plane checks | `scenarios/edge.go`: `docker exec bnk-edge-frr-<cluster>` | only exists on a tool-built cluster |
| Assumptions | `bnk-gatewayclass`, VIPs `203.0.113.10x`, `scn-*` namespaces, dataplane mode read from `poc.yaml` | wrong on a foreign cluster |

## Target shape

```
kit/
  AGENTS.md                  # entry point for agents and humans
  env/ocibnkctl.env          # defaults matching a tool-built cluster
  env/example-external.env   # template for a 3rd-party cluster
  lib/common.sh              # render, wait, assert helpers; assertions as JSON lines
  lib/edge.sh                # pluggable client/peer drivers
  lib/preflight.sh           # capability checks against the *cluster*
  platform/
    prereqs/ flo/ shrink/ cne/ cwc/   # manifests + values + install.sh per phase,
                                      # README.md per phase carrying today's rationale
  scenarios/<slug>/
    README.md        # the how-to, what's tested, manual steps, rating
    scenario.yaml    # name, title, rating, deps, requires: [...], params+defaults
    manifests/*.yaml # ${VAR} placeholders
    apply.sh  verify.sh  cleanup.sh
  kit.go             # package kit; go:embed — the binary ships the same bytes
```

### Design decisions

1. **One source, embedded both ways.** `go:embed` cannot reach parent
   directories, so `kit/kit.go` (package `kit`) embeds the tree and the rest
   of the binary imports it. Binary and hand-operator use identical bytes.
2. **Templating: `${VAR}` for scalars, kustomize overlays for structure.**
   `{{if .NetworkAttachments}}` in the CNEInstance becomes `cne/base` plus
   `overlays/anycast-bgp`, `overlays/small-host` — plain `kubectl apply -k`.
   Substitution always uses an explicit variable list so `$` inside
   annotations and embedded scripts survives. macOS lacks `envsubst`
   (gettext), so `lib/common.sh` ships a small bash `render` function rather
   than adding a dependency.
3. **Verify contract = JSON lines.** `verify.sh` prints one
   `{"description","ok","got"}` object per check — exactly the existing
   `scenarios.Assertion` struct. A generic Go `shellScenario` adapter runs kit
   scenarios through the existing runner, so reports, `--all` topo-sort and
   rollups keep working. Humans and agents just read the output.
4. **Edge drivers** (`EDGE_DRIVER`) make data-plane checks portable:
   - `ocibnkctl` — today: `docker exec` into the external FRR container.
   - `pod` — deploy an in-cluster FRR/netshoot pod on a user-named NAD.
   - `external` — user supplies peer IP/ASN and a host/command to curl from.
   - `none` — control-plane checks only; data-plane assertions are reported
     **skipped**, not failed.
5. **Preflight reads the cluster, not `poc.yaml`.** A scenario's
   `requires: [gateway-api, cneinstance, anycast-bgp]` is answered from
   cluster state (e.g. the CNEInstance's `TMM_MAPRES_ADDL_VETHS_ON_DP` env).
6. **Secrets never land in rendered output by default.** The FAR pull secret
   and License JWT render as placeholders plus the command that fills them.

### What stays Go (for now)

The native k3s provisioner (`cluster up/scale/destroy`), and optionally the
e2e resume/report runner as a convenience over the kit. Whether to script the
provisioner too is deferred until there's real kit usage.

## Phases

### Phase 0 — export what exists (no refactor)

`ocibnkctl render --poc . --out <dir>` writes every manifest the binary
would apply, laid out by phase, plus a generated `README.md` giving the apply
order and the exact helm/kubectl commands:

- `00-prereqs/`: namespaces, FAR pull-secret (placeholder), cert-manager helm command
- `10-flo/`: bnk-ca issuer chain, FLO values, helm command, CWC cert-gen steps
- `15-shrink/`: Kyverno helm command + shrink ClusterPolicy (when the PoC needs it, or `--all-modes`)
- `20-cne/`: Multus/whereabouts sources (pinned URL + sha256), NAD(s), OcNOS
  template, CNEInstance, License (placeholder), GatewayClass, Infra
- `scenarios/<name>/`: each scenario's rendered manifests + a README with
  title, rating, dependencies and description

No cluster, no network, no FAR key needed. `--include-secrets` writes the
real FAR secret and JWT (0600) when the keys are present. This immediately
answers "just give me the FLO manifest", and its output becomes the golden
reference for Phase 1.

**Exit:** `make smoke` renders a temp PoC and asserts the key files exist.

### Phase 1 — move the data out of Go (no behaviour change)

Move `internal/embedded/templates/*`, every scenario `manifests/` dir and the
`Sprintf` manifests into `kit/`; convert Go templates to `${VAR}` + guarded
blocks; Go renders through the same path.

**Exit:** Phase 0 output is byte-identical before/after. `make smoke` checks
that kit YAML parses and that no `${` is left unresolved.

**Done (2026-10-28).** As built:

- **Guarded blocks, not kustomize overlays.** Tested: kustomize drops every
  comment and re-sorts keys, which would throw away the templates'
  explanations and rule out byte-identical parity, and Go could only use it
  by shelling out to `kubectl`. Instead, optional sections sit between
  `#@if NAME` / `#@end` comment lines. Only two templates need them
  (CNEInstance: `networkAttachments`, `aiGateway`, `consoleURL`; License:
  `jwksConfigMapRef`); everything else is `${NAME}` substitution.
- **One renderer, two implementations.** `kit.Render` (Go) and
  `kit/lib/render.sh` (POSIX sh + awk, no `envsubst`, so macOS works);
  `TestRenderShellParity` runs every kit template through both, under every
  awk installed.
- **Golden files** (`internal/cli/testdata/render/`, five PoC shapes) were
  captured before the move; every platform manifest came out byte-identical.
  The deliberate diffs: the two scenario `.tmpl` files switched from
  `{{.Field}}` to `${NAME}`, the bundle ships `lib/render.sh` and uses it
  instead of `envsubst`, and render now emits the test-JWT JWKS ConfigMap
  that `deploy cne` had been applying since the tst-JWKS change (a real
  drift between the two paths, which this phase exists to prevent).
- **Checks:** every kit YAML parses (templates after rendering);
  `kit/README.md` must document every platform file. kubeconform schema
  validation is not added: it isn't installed here, and smoke should not
  depend on it.
- **Deferred to Phase 3:** manifests that Go *generates* rather than fills
  — loops in `routingtemplate`/`routingconfigtenant`, the inline manifests in
  `grpcroute`, `corefiles`, `aitokencountdssm`, `aiepp` and `llmgateway` —
  move when those scenarios are ported. `cluster/edge.go` and `k3s.go` hold
  container config, not Kubernetes manifests, and stay. `RenderFRRPeer`,
  `RenderFRRStaticNAD` and `RenderBGPNAD` are unused leftovers of the
  in-cluster FRR and were left in place.

### Phase 2 — shell scenario contract + pilots

`lib/*.sh`, the `shellScenario` adapter, the edge drivers. Three pilots chosen
to span the space:

- `cwc-admin-access` — no edge needed
- `cluster-wide-watch` — control-plane only
- `http-routing-e2e` — edge + BGP

The in-flight `aiepp` / GAIE scenario is a good first *kit-native* scenario.

**Exit:** each pilot passes via `ocibnkctl scenario run` **and** via bare
`kit/scenarios/<x>/verify.sh` on a tool-built cluster.

**Done (2026-10-28).** As built:

- **Layout:** `kit/scenarios/<name>/` = `scenario.yaml` (metadata, read by
  Go), `scenario.env` (variables + defaults, sourced by the scripts; the
  environment wins), `apply.sh` / `verify.sh` / `cleanup.sh`,
  `manifests/`, `README.md`. A scenario becomes a script scenario by
  having a `verify.sh`; `internal/scenarios/shell.go` registers it. No Go.
- **Contract** (`kit/lib/common.sh`): `verify.sh` prints one
  `{"description","ok","got","skipped"}` JSON line per check and a
  `{"summary"}` line, exit 0 / 1. `Assertion` gained `Skipped` (counts as
  passed, shown as `–`). Helpers: `kit_check` / `kit_eq` / `kit_contains`
  / `kit_skip`, `kit_retry`, `kit_apply` (renders `.tmpl`),
  `kit_copy_secret`, `kit_require` (preflight from the cluster: CRDs,
  GatewayClass Accepted, Secrets, anycast-bgp from the CNEInstance's
  mapres env).
- **Edge drivers** (`kit/lib/edge.sh`): `none` (default by hand),
  `ocibnkctl` (set by `scenario run` unless the operator set
  `EDGE_DRIVER`), `external` (`EDGE_EXEC` / `EDGE_VTYSH` prefixes). The
  `pod` driver moves to Phase 3 with the portability test: it needs an
  in-cluster FRR on a NAD the operator names.
- **Single-sourced OcNOS redistribute:** `kit/lib/ocnos-redistribute.cfg`
  is read by both `deploy.OcNOSRedistributeScript` (byte-identical, per
  the golden files) and `kit/lib/bnk.sh`.
- **Pilots ported, Go packages deleted:** `cwc-admin-access`,
  `cluster-wide-watch`, `http-routing-e2e` (its VIP is now `${VIP}`).
  Assertion descriptions match the Go versions so reports stay
  comparable.
- **Render bundles** now copy script scenarios whole and give every
  scenario a `manifests/` subdirectory (the kit's layout); all 144 moved
  files are byte-identical to before.
- **Tests:** `shell_test.go` runs the pilots' real scripts against stub
  `kubectl` / `docker` (edge `none` and `ocibnkctl`, a failing check, a
  failed preflight, the CWC token gate).
- **Exit check, live** on a BNK 2.4.1 anycast-bgp cluster built by
  ocibnkctl: all three pilots green via `ocibnkctl scenario run`
  (cwc-admin-access 4/4, cluster-wide-watch 5/5, http-routing-e2e 6/6
  including BGP, kernel route and 5/5 curls through TMM), and again bare
  from the repo's `kit/` with only `KUBECONFIG` set: `EDGE_DRIVER=none`
  (3 passed, 3 skipped) and `EDGE_DRIVER=external` pointed at the same FRR
  (6/6). `cleanup.sh` and `scenario clean` both removed everything; the
  VIP was withdrawn from BGP ~10s later.

### Phase 3 — port the rest, prove portability

Port remaining scenarios; delete each Go package at parity. Hardest last:
`bgp-peer-frr`, `routing-template-*` (OcNOS imish exec, passwd.conf
injection).

**Exit:** one run on a cluster `ocibnkctl` didn't build, with
`EDGE_DRIVER=none` and then `pod`.

In batches, one PR each:

| Batch | Scenarios | Status |
|---|---|---|
| A | `bgp-peer-frr`, `tcp-l4-loadbalance`, `udp-l4-loadbalance`, `ai-token-counting`, `fic-dynamic-ip`, `proxy-protocol-l4` | done, verified live |
| B | `external-resource-pool`, `ai-semantic-cache`, `grpc-loadbalance`, `ai-token-counting-dssm`, `core-file-collection`, `bgp-anycast` | done; five verified live, `core-file-collection` stub-tested only (it permanently changes the cluster) |
| C | `ai-inference-epp`, `llm-gateway` | done, verified live |
| C2 | `routing-template-multitenant`, `routing-config-tenant` (amber) | done, verified live on selfip-dag |
| D | `pod` edge driver; third-party cluster run (the exit check) | `pod` driver done, verified live; third-party run open (no such cluster available) |

Batch A notes: `bgp-peer-frr` was moved forward rather than left for last —
every data-plane scenario depends on it, and once it is a script the rest of
the family needs no Go at all. The repeated "wait for the VIP in the peer's
BGP table, retriggering OcNOS redistribute" loop became
`edge_check_vip`, the N-curl loop `edge_curls`, and N-of-N checks
`kit_count`. OcNOS's `passwd.conf` line joined the redistribute config in
`kit/lib/` (read by `deploy cne` and `bnk.sh`). Preflight gained
`gateway-settings` (fic-dynamic-ip). Each Gateway VIP is now `${VIP}`.
`TestShellScenariosHealthy` runs every script scenario's real scripts
against one stub cluster, under both edge drivers.

The live run (BNK 2.4.1, anycast-bgp, AI gateway with metrics) found two
things the Go originals shared: `bgp-peer-frr` only recognised the Up/Down
column as `hh:mm:ss`, which FRR prints for the first 24h of a session only
(`2d17h34m` after), so it failed on any long-running cluster — it now keys on
the State/PfxRcd prefix count. And `ai-token-counting`'s iRule-log check
cannot pass with `telemetry.loggingSubsystem` enabled (TMM's iRule lines are
not in `kubectl logs` then; `main`'s Go version fails it identically there),
so it is now reported skipped, with that reason, on such clusters. After the
fixes all six pass via `ocibnkctl scenario run` and bare (`EDGE_DRIVER=none`).

Batch B notes: every Gateway VIP is now a variable (the DSSM scenario's four
too). `grpc-loadbalance` fetches the SHA-256-pinned grpcurl release with
`kit_fetch` into `KIT_CACHE` (`ocibnkctl` points it at
`<PoC>/artifacts/bin`, as the Go version did) and mounts it into the client
via `EDGE_RUN_ARGS`; an external client brings its own. The DSSM delta check
is awk over before/after snapshots and the backend's sums, comparing only
buckets the backend reported, like the Go version. `core-file-collection`
patches the live CNEInstance from `cneinstance-patch.yaml` and, as before,
never reverts (its `cleanup.sh` says why). `bgp-anycast` shares
`edge_bgp_wait` with `bgp-peer-frr` and reads the ECMP path count from FRR's
own `Paths: (N available` line.

Live (BNK 2.4.1, anycast-bgp, one TMM worker): the five non-invasive ones
pass via `ocibnkctl scenario run` — DSSM counters matched the backend's
usage in all 24 buckets over 40 requests, grpcurl was fetched and verified —
and bare with `EDGE_DRIVER=none`. With one worker `bgp-anycast`'s ECMP check
is trivially satisfied; it still wants a run on a multi-worker cluster with
an edge router.

Batch C notes: `ai-inference-epp` fetches the GAIE CRDs with `kit_fetch`
(the pin moved from `internal/version` to its `scenario.env`, and
`deploy.EnsureGAIECRDs` went with its only user); the tmctl and Prometheus
parsers became awk, tested against the captured fixtures the Go tests used.
`llm-gateway`'s manifests used Go-template `{{.Field}}` syntax without a
`.tmpl` suffix — fine through ocibnkctl, which rendered them, but not
usable by hand from `kit/` — and are now `${VAR}` templates. Its virtual key
goes to `KIT_STATE` (ocibnkctl: `<PoC>/artifacts`, so
`artifacts/llm-gateway/virtual-key`, where the profile's `ask.sh` reads it);
the admin password and provider key only pass through 0600 files and
pipes. Preflight gained `ai-gateway`, read from the CNEInstance.

Live (BNK 2.4.1, AI gateway with metrics): `ai-inference-epp` 20/20 — all
10 requests on the idle pod, 10 EPP consults, 10 prefix-index lookups;
`llm-gateway` 7/7, including the f5ingress 2.4.1 profile race (the
controller restart recovered it) and ClickHouse records for 200, 401 and
403. Both pass bare with `EDGE_DRIVER=none`.

Batch D notes: the `pod` driver is a client, not a BGP peer. The plan had
an in-cluster FRR on a NAD, but peering it means adding a neighbor to TMM's
OcNOS template — a platform change on the cluster under test. A netshoot
pod on a NAD that reaches TMM's data plane, with static routes for the VIP
range via a TMM's address on it (read from TMM's Multus network-status),
needs nothing from the cluster but the NAD, and runs every data-plane check
(curl, socat, grpcurl via `edge_tool`, which now also copies tools into the
pod). BGP-table checks still need a router CLI (`EDGE_VTYSH`). With several
TMMs the route goes to one of them: no ECMP, which only `bgp-anycast`
checks, through the router.

Live (BNK 2.4.1, anycast-bgp), bare from `kit/` with only `KUBECONFIG` and
`EDGE_DRIVER=pod`: the pod took 192.168.95.161 on `default/bnk-bgp` and
routed the VIPs via TMM's auto-detected .160; http-routing-e2e 5/5 curls,
tcp-l4 20/20, udp-l4 5/5, grpc-loadbalance list + unary over L4 (grpcurl
copied into the pod), ai-token-counting-dssm 40/40 with all 24 DSSM buckets
matching; only BGP-table checks skipped. That is the closest this setup can
get to the exit check: the scripts never touched the ocibnkctl edge fabric.

### Phase 4 — platform install as scripts

`kit/platform/<phase>/install.sh` for prereqs → flo → (shrink) → cne,
including release-manifest resolution via `helm` against
`f5-bigip-k8s-manifest`. Go `deploy *` shells out to them. Retry/ordering
subtleties move into the scripts, with today's comments moved verbatim into
each phase README.

**Exit:** full `e2e` + `scenario run --all` stay green. Riskiest phase.

**Done (2026-10-28), in one step** (scripts and the Go switch together, as
chosen). As built:

- `kit/platform/<phase>/install.sh` + `platform.env` (variables, defaults
  of a default PoC) + `lib.sh`. The scripts carry the ordering and retry
  rationale as comments; the per-phase READMEs the plan mentioned became the
  "Installing BNK with the scripts" section of `kit/README.md`.
- `ocibnkctl deploy *` keeps the gates, poc.yaml mapping (`platformEnv`),
  reachability and status, and runs the scripts; ~2,200 lines of Go deploy
  logic are gone. `render` keeps the Go renderers; `TestPlatformCNEMatchesGo`
  (the scripts' CNEInstance == Go's for every golden PoC shape) and
  `TestPlatformEnvMatchesGo` (platform.env defaults == ocibnkctl's pins)
  guard the two from drifting.
- Gate, on a fresh PoC (BNK 2.4.1, anycast-bgp, one worker, test JWT):
  `e2e` green in ~6 min (prereqs 16s, flo 12s, cne 4m42s; shrink correctly
  skipped), no warnings, License Active via the JWKS path, the applied
  CNEInstance and FLO values byte-identical to `ocibnkctl render`'s; then
  `scenario run --all` 16/16 for every scenario the PoC supports — including
  `core-file-collection` and both AI iRule-log checks, which ran in full
  there (no logging subsystem). `llm-gateway` failed its `ai-gateway`
  preflight because that PoC has no AI gateway, exactly as the Go version
  did: `--all` does not filter scenarios by capability (a follow-up: report
  such a scenario as skipped).

### Phase 5 — shrink the binary to what's hard in shell

Binary = k3s provisioner + optional runner. Add `ocibnkctl kit extract
<dir>`; publish `kit/` as a release tarball usable with no binary. Rewrite
the PoC `AGENTS.md` around the kit, with the binary as an accelerator.

**Done (2026-10-28).** By now the binary is already what this phase asked
for: `cluster up/scale/destroy` (the k3s provisioner), thin `deploy`
wrappers that gate, map poc.yaml and run the platform scripts, the scenario
runner and reports, and `render`. Added here:

- `ocibnkctl kit extract <dir>` — the whole kit (`kit.Tree`: README.md,
  lib, platform, scenarios), scripts executable; refuses to overwrite
  anything but a previous extract.
- `bnk-kit_<version>.tar.gz` with every release: `make kit-tarball` (kit/
  without its Go files, under `bnk-kit/`) runs as a goreleaser before
  hook; the tarball is a release extra file and in `checksums.txt`.
  Validated with goreleaser `check` and a `--snapshot --skip=publish` run.
  `TestKitTreeIsWhatShips` keeps the tarball's contents equal to
  `kit.Tree`; an extracted kit was diffed identical to the tarball.
- The PoC's `AGENTS.md` gains "The kit: the same steps as plain scripts":
  what is where, when to use it, and that on the PoC itself the phases
  still go through `ocibnkctl` (gates, poc.yaml, journal).

Still open from the earlier phases: the third-party cluster run.

**Batch C2 (2026-10-28), on a throwaway selfip-dag PoC.** The routing pair
is ported and the last Go scenario packages are gone (`internal/scenarios`
now holds only the framework, the shell adapter and three edge helpers).
The same cluster gave the platform scripts' `selfip-dag` branch its first
live run: `e2e` green (cne 5m36s) with the DAG NAD, F5SPKVlan Programmed,
Infra correctly skipped, License Active. Then: routing-template-multitenant
and routing-config-tenant green, including the ARP next-hop and
tenant-isolation probes from the node netns; `scenario run --all` skipped
every anycast-bgp / GatewaySettings / AI-gateway scenario as not
applicable and passed cwc-admin-access, cluster-wide-watch and
core-file-collection. Two defects found there and fixed: the isolation
probe misread iputils arping's `Received 0 response(s)` (the stub now
serves that captured output), and `http-routing-e2e` lacked the
anycast-bgp preflight the Go version had, so it ran and failed instead of
being skipped (`TestDataPlaneScenariosSkipOutsideAnycast` now requires the
preflight of every scenario depending on `bgp-peer-frr`).

## Open questions

- **Kit location:** `kit/` in this repo (recommended while parity tests
  matter) vs. a separate repo once stable.
- **Rating for `EDGE_DRIVER=none` runs:** does control-plane-only count as
  green, or does it get its own rating (e.g. `green-cp`) so third-party users
  know what was actually exercised?
