# Plan: from ctl binary to agentic kit

Status: **proposal** — Phase 0 (`ocibnkctl render`) implemented 2026-10-28.

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
`Sprintf` manifests into `kit/`; convert Go templates to `${VAR}` + overlays;
Go renders through the same path.

**Exit:** Phase 0 output is byte-identical (or `kubectl kustomize`-diff
equal) before/after. `make smoke` gains `kubeconform` over `kit/` and a
"no unresolved `${`" check.

### Phase 2 — shell scenario contract + pilots

`lib/*.sh`, the `shellScenario` adapter, the edge drivers. Three pilots chosen
to span the space:

- `cwc-admin-access` — no edge needed
- `cluster-wide-watch` — control-plane only
- `http-routing-e2e` — edge + BGP

The in-flight `aiepp` / GAIE scenario is a good first *kit-native* scenario.

**Exit:** each pilot passes via `ocibnkctl scenario run` **and** via bare
`kit/scenarios/<x>/verify.sh` on a tool-built cluster.

### Phase 3 — port the rest, prove portability

Port remaining scenarios; delete each Go package at parity. Hardest last:
`bgp-peer-frr`, `routing-template-*` (OcNOS imish exec, passwd.conf
injection).

**Exit:** one run on a cluster `ocibnkctl` didn't build, with
`EDGE_DRIVER=none` and then `pod`.

### Phase 4 — platform install as scripts

`kit/platform/<phase>/install.sh` for prereqs → flo → (shrink) → cne,
including release-manifest resolution via `helm` against
`f5-bigip-k8s-manifest`. Go `deploy *` shells out to them. Retry/ordering
subtleties move into the scripts, with today's comments moved verbatim into
each phase README.

**Exit:** full `e2e` + `scenario run --all` stay green. Riskiest phase.

### Phase 5 — shrink the binary to what's hard in shell

Binary = k3s provisioner + optional runner. Add `ocibnkctl kit extract
<dir>`; publish `kit/` as a release tarball usable with no binary. Rewrite
the PoC `AGENTS.md` around the kit, with the binary as an accelerator.

## Open questions

- **Kit location:** `kit/` in this repo (recommended while parity tests
  matter) vs. a separate repo once stable.
- **Rating for `EDGE_DRIVER=none` runs:** does control-plane-only count as
  green, or does it get its own rating (e.g. `green-cp`) so third-party users
  know what was actually exercised?
