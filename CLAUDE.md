# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this binary is

`ocibnkctl` is a single-binary Go CLI that drives a full F5 BIG-IP Next for
Kubernetes (BNK) 2.4.0 deployment onto a native k3s cluster: one **dedicated
control node** (server, tainted `control-plane:NoSchedule`) plus **N worker
nodes** (`cluster.tmm_nodes`), each labelled `app=f5-tmm`. TMM runs in **demo
mode** (virtio inside the pod netns, no DPU/SR-IOV) as a FLO **wholeCluster
DaemonSet** — one TMM per labelled worker — so the data plane scales out with
the worker count. The default data-plane mode is **anycast-bgp** (each TMM
gets a net1 via Multus + whereabouts IPAM on the `bnk-bgp` NAD and advertises
its VIP /32 over BGP). It is a copy-fork of
[`dpubnkctl`](https://github.com/mwiget/dpubnkctl) with the
bare-metal/DPU/kubespray pipeline replaced by native k3s-in-containers, but
with `internal/deploy` and `internal/bnkforge` forked verbatim (with local
kubectl/helm instead of containerized). It is itself the successor to
`kindbnkctl` (the kind/k3d-backed predecessor).

Versions are pinned in `internal/version/version.go` and stamped into the
binary via `-ldflags` (see Makefile). Important nuance: only the *cluster-side*
pins live there (k3s node image, Calico, cert-manager, Kyverno, whereabouts —
the latter SHA-pinned). The **FLO / CIS / cert-gen chart and image versions are
deliberately NOT pinned in Go** — they are resolved at deploy time from the
`f5-bigip-k8s-manifest` release-manifest chart on `repo.f5.com`, keyed off
`version.CNEManifestVersion` (`internal/deploy/manifest.go`). A BNK bump is
therefore usually a one-line `CNEManifestVersion` change, not a sweep of chart
versions — verify it first with `ocibnkctl manifest probe` (no cluster needed).
Since 2.4.0 the tag is the plain product version (`2.4.0`); the 2.3 line used
`<ver>-3.2598.3-0.0.NNN`. Doc-vs-chart tag divergence has happened before, so
the probe is the source of truth, not the docs.

**Cluster backend.** A single native **k3s** backend (`internal/cluster/k3s.go`,
implementing the `Provisioner` interface). It runs `rancher/k3s` server +
agent containers directly on the host OCI runtime (docker or podman) via the
runtime CLI — **no kind, no k3d, no third-party orchestrator binary**.
`CreateCluster` starts every node through a boot-script entrypoint
(`k3sNodeBootScript`). On each container start it re-applies the host fixups:
rootfs `rshared` for Calico's `mount-bpffs` (plain `docker run` is `rprivate`),
`/var/run`, `rt_tables`, `core_pattern`, and boot hooks such as the worker edge
bridge. A cluster therefore survives a runtime restart. It joins
the agent over a per-cluster bridge network with a shared token, and extracts
the kubeconfig via `docker exec`, rewriting it to the host-mapped API port.
k3s's bundled flannel/traefik/servicelb are disabled so Calico is the CNI;
the result is a Calico-CNI v1.30.14 cluster of 1 control node + N workers.
k3s leaves the server node schedulable (unlike kind), so `cluster up`
**taints it** `control-plane:NoSchedule` to dedicate it — all BNK workloads
(and the TMM DaemonSet) then land only on the labelled worker(s).

## Build / test / run

```bash
make build               # darwin/amd64 host build → bin/ocibnkctl
make build-all           # host + linux-arm64
make release             # versioned + sha256 release artifacts for linux-amd64 + darwin-arm64
make install             # → ~/.local/bin/ocibnkctl
make test                # go test ./...
make smoke               # unit tests + Layer-A CLI smoke (no cluster, ~5s) — the gate before pushing
make fmt vet tidy
make runner-image RUNNER_VERSION=2.4.0-1 PUSH=1   # BNK Forge container-runner image
```

Release tagging is unusual and deliberate: the binary is hard-pinned to one BNK
release, so **every tag carries the `v<BNK>` prefix** and tool-level changes get
an incrementing suffix rather than a new semver — `v2.4.0`, `v2.4.0-1`,
`v2.4.0-2`. Pushing any `v*` tag triggers the goreleaser workflow
(`.github/workflows/release.yml`), which is the canonical release path;
`make release` is the manual fallback. Prior BNK lines live on
`release/2.3.2`, `release/2.3.1`, `release/2.3.0`. Full publish chain (binary → runner image → `bnkctl-index`
digest → BNK Forge) is in `docs/RELEASE.md`.

Run one test:

```bash
go test ./internal/poc -run TestValidate
go test ./internal/deploy -run TestLicenseCR -v
```

`make smoke` is the canonical pre-push check: it runs `go test ./...` **and**
exercises the built binary end-to-end against a temp PoC (init → validate →
e2e --dry-run → doctor) with assertions on every step. Don't push without it.

## The PoC pattern (important — affects every command)

A "PoC" is a local directory on disk created by `ocibnkctl init <name>`. It
holds the declarative state for one cluster:

```
poc.yaml         # source of truth (see internal/poc/schema.go)
AGENTS.md        # embedded operator guide (also @-included by CLAUDE.md)
journal/         # append-only markdown log of runs
artifacts/       # rendered k3s.yaml, kubeconfig, helm values, CWC certs
keys/            # gitignored — FAR tgz + JWT live here
```

Every CLI subcommand takes `--poc <dir>` (defaults to `.`). The PoC dir is
both *input* (poc.yaml, customer keys) and *output* (artifacts/, journal/),
and it's resume-safe: each phase is idempotent. The scaffolding for new
PoCs lives in `internal/embedded/files/` and `internal/embedded/templates/`
and is shipped inside the binary via `go:embed` (`internal/embedded/`).

## Pipeline shape

```
validate → cluster up → deploy prereqs → deploy flo → [deploy shrink] → deploy cne
```

Each phase is idempotent and gated by `--yolo` plus a typo-guard:
`--confirm-cluster <name>` (cluster mutations) or `--confirm-deploy <name>`
(in-cluster mutations) must echo the PoC name. `e2e` chains all phases
(`internal/cli/e2e.go`, `canonicalPhases`): it is resume-safe (per-phase state
persisted; `--no-resume` to force), supports `--phase a,b,c` and `--dry-run`,
and writes `reports/<ts>/logs/NN-<phase>.log` plus an aggregated
`run-<poc>-<ts>.{json,md}`.

`deploy shrink` is the one **conditional auto phase** — on a full `e2e` it
engages unprompted only when `runtime.NumCPU()` is below
`version.FloorForWorkers(tmm_nodes)` **or** the runtime's memory (`docker info`
MemTotal — the Docker Desktop VM, not host RAM) is below
`MinBaseline.MemoryGB` (`version.MemoryBelowFloor`); naming it via
`--phase deploy-shrink` always runs it. It sits between `flo` and `cne` on purpose: Kyverno must be
admitting before `deploy cne` creates the TMM/DSSM pods, so they land
pre-capped instead of wedging the readiness wait on Insufficient CPU.

`destroy` runs them in reverse: bnk-forge unregister → remove k3s node containers
→ remove the cluster's docker network.

### Host resource floor (why `shrink` exists)

The floor is dictated by scheduling `requests`, not real RSS (~6.7 Gi actual vs
a 10-core/16 GB reservation floor) — see the long comment on
`version.MinBaseline`. Every k3s node is a container on the *same* host, so an
extra TMM worker adds no capacity, just another full-fat TMM: hence
`PerExtraTMMNodeCores = 8` and `FloorForWorkers()`. Memory binds per node:
with the control node tainted, the single TMM worker must hold every BNK
request (~20.2 Gi on 2.4.0), so a 16 GB VM needs shrink even at 10 cores —
`MinBaseline.MemoryGB = 24`. `doctor` fails on cores and warns on memory.

`deploy shrink` (`internal/deploy/shrink.go`) lowers the footprint via a
**Kyverno mutating admission policy**, not via chart values — FLO owns every
workload spec through server-side-apply and reasserts it on a tight reconcile
loop, so admission (which runs after FLO's apply) is the only durable layer.
`bnk.host_profile: small` is the Raspberry-Pi-class 4-core profile
(`MinBaselineSmallHost`); it assumes both the shrink policy *and*
`telemetry.metricSubsystem=false`.

The deploy phase composes: cert-manager via helm, Multus + whereabouts
(cluster-wide IPAM for per-TMM net1) and the `bnk-bgp` NAD for the default
anycast-bgp data plane, the FLO chart pulled at deploy time from the BNK
release manifest at `repo.f5.com`, and a `CNEInstance` CR with
`wholeCluster: true` + `advanced.demoMode.enabled: true` (TMM runs as a
DaemonSet on the `app=f5-tmm` workers — no `tmmReplicas`). The CWC cert-gen
step shells into an `alpine/k8s:1.31.5` container — that container image is a hard runtime
dependency at deploy time.

## Package layout

```
cmd/ocibnkctl/        main entrypoint (just wires internal/cli.NewRootCmd)
internal/cli/          cobra subcommands — root.go assembles the tree
internal/poc/          poc.yaml schema + I/O + validation
internal/cluster/      native k3s backend (Provisioner) + docker/podman wrappers
internal/deploy/       cert-manager, FLO, License CR, CWC cert-gen, Runner (kubectl/helm wrapper)
internal/scenarios/    test-case framework + per-scenario subpackages (see below)
internal/bnkforge/     bnk-forge HTTP client (copy-fork from dpubnkctl)
internal/runtimeenv/   host-vs-container detection (leaf pkg, stdlib only)
internal/embedded/     go:embed of AGENTS.md, CLAUDE.md, templates/
internal/version/      build-stamped version + BNK 2.4.0 pins + min-spec floor
```

**`internal/runtimeenv` matters more than its size suggests.** `ocibnkctl` is a
host tool by design, but it also ships as a BNK Forge container-runner image
(`runner.Dockerfile`), where host assumptions break — loopback addresses, and
`docker run -v <local-path>` binding a path the host daemon cannot see. Both
`cluster` and `deploy` branch on `runtimeenv.InContainer()`; `cluster` also uses
`SelfContainerID()` to attach itself to the cluster's docker network
(`internal/cluster/incontainer.go`) so kubectl can reach the node containers.

Relatedly, `init` accepts **env overrides** (`OCIBNKCTL_CUSTOMER`,
`_PROVIDER`, `_TMM_NODES`, `_EDGE_OCTET`, `_HOST_PROFILE`, `_TEEMS_RELAY` —
`internal/cli/initenv.go`) so argv+env runners can scaffold a non-default PoC
non-interactively. They only *seed* poc.yaml, which stays the source of truth.

## Scenarios framework

`internal/scenarios/` is a separate test-case system layered on top of
the deployed cluster. Each scenario maps to one article in the
[F5 BNK how-tos index](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/),
renders manifests into `artifacts/scenarios/<name>/`, applies them, asserts
reconciled state, and writes a JSON+md report under `reports/<timestamp>/`.

Scenarios self-register at `init()` time via `scenarios.Register(s)` and
implement the `Scenario` interface in `internal/scenarios/scenario.go`:
`Manifests` (pure render) → `Apply` → `Verify` → `Cleanup`. Each lives in
its own subpackage (`aitokencount/`, `bgppeer/`, `httproutee2e/`, etc.).

`Dependencies()` is the ordering contract: `scenario run --all` topo-sorts the
green scenarios by it (`topoSortByDeps` in `internal/cli/scenario.go`) so e.g.
`bgp-peer-frr` comes up before `http-routing-e2e`. A single-name
`scenario run` does **not** auto-chain — a missing dep surfaces as a failed
assertion, leaving the call to the operator. `Verify` reports rich
`Assertion{Description, OK, Got}` values so a report reader can see what failed
without re-running; a failed assertion does not short-circuit `Verify`.
Subcommands: `scenario list` / `run [name|--all]` / `clean [name]`.

Ratings — set by the scenario itself, only after it's been run:

| Rating | Meaning |
|---|---|
| **green** | fully testable in the demo-TMM shape (1 control node + N TMM workers) |
| **amber** | control-plane verifies; data-plane plumbing partially missing (a real BNK 2.3 gap, the k3s shape, or both — see the scenario's `Description()` for which) |
| **red**   | requires DPUs / real upstream BIG-IP / bondable NICs — listed for discoverability, never executed |

When adding a new scenario, the convention is: subpackage under
`internal/scenarios/<slug>/` exporting a `New()` constructor, registered via
the package's `init()`. Wire it into the CLI by importing the new subpackage
from `internal/cli/scenario.go`'s import block — registration happens as a
side effect of the import.

## BGP / NAD detail (matters when touching scenarios)

`bgp-peer-frr` (and everything that builds on it — `http-routing-e2e`,
`external-resource-pool`, `proxy-protocol-l4`) deploys a real BGP session
between an FRR pod and TMM's ZeBOS daemon over a Multus NAD on a per-node
Linux bridge (`bnk-bgp` / `br-bnk-bgp`), bypassing TMM's eth0 TCP hook
entirely. Gateway IPs (`203.0.113.100/101/102`) are advertised via
`redistribute kernel` at router-bgp scope and installed by FRR as kernel
routes — that's how data-plane curls reach Gateways without going through
TMM's userspace TCP path.

The critical knob is
`CNEInstance.spec.advanced.tmm.env TMM_MAPRES_ADDL_VETHS_ON_DP=FALSE`
(set by `bgp-peer-frr`). With the default `TRUE`, mapres grabs `net1` for
the userspace data plane and flushes its kernel IP, breaking ZeBOS source-
binding. The full topology diagram is in README.md "Network topology".

## bnk-forge integration

Optional. If `~/git/bnk-forge` (or `$OCIBNKCTL_BNK_FORGE_PATH`) exists at
`init` time, the `bnk_forge:` block is pre-filled and `cluster up` best-effort
registers the k3s cluster with bnk-forge. If bnk-forge isn't running, the
hook logs a clean skip and continues — deployment never blocks on it.
`ocibnkctl` will not install or start bnk-forge.

## Local registry cache (optional)

Repeated cluster create/destroy cycles re-pull the same images (each k3s node's
containerd store is wiped on `destroy`). The companion **`regcachectl`** tool
([separate repo](https://github.com/mwiget/regcachectl), `../regcachectl`) runs a
host-local pull-through cache fleet —
anonymous `registry:2` caches for `docker.io`/`ghcr.io`/`quay.io`/`nvcr.io` and a
**credential-free blob cache** for `repo.f5.com`. The mirror table in
`internal/cluster/registries.go` must match regcachectl's `Upstreams` order
(ports are `port_base+i`), so `nvcr.io` is listed even though BNK never pulls
from it. Opt a cluster in via `poc.yaml`:

```yaml
cluster:
  registry_cache:
    enabled: true            # default false → direct pulls, unchanged
    host: host.docker.internal   # optional
    port_base: 5000              # optional
```

`cluster up` (and `scale`) then render `artifacts/registries.yaml` (each upstream
→ cache, real upstream as fallback; plus a `configs:` block carrying THIS PoC's
FAR key for the credential-free F5 cache) and bind-mount it into every node.
The cache stores no secret; each cluster supplies its own key, so GA vs
engineering builds share one cache. Renderer/mount: `internal/cluster/
{registries,k3s}.go`; wiring: `applyRegistryCache` in `internal/cli/backend.go`.

## Customer-supplied secrets

`keys/f5-far-auth-key.tgz` (FAR image-pull tarball for `repo.f5.com`) and
`keys/.jwt` (TEEM activation token) must be dropped into `keys/` by the
operator before any deploy phase. `keys/` is gitignored in scaffolded PoCs.
These come from F5's normal license-portal channels; never check them in.

## Where the long-form reasoning lives

CLAUDE.md stays short on purpose; the deep write-ups are checked in:

- `README.md` — "Network topology", quick start, per-phase invocation,
  scaling, the `~/.kube/config` lifecycle, the full scenario table.
- `docs/dataplane-modes.md` — the three TMM dataplane modes side by side, and
  why `anycast-bgp` needs no `F5SPKVlan` (the pod IP *is* the self-IP).
- `docs/RELEASE.md` — tag → binaries → runner image → `bnkctl-index` → Forge.
- `docs/grpc-route-investigation.md` — worked example of how a scenario's
  rating gets established (and why GRPCRoute's data plane is still red).
- `docs/rpi-e2e-performance.md` — measured small-host (`host_profile: small`) run.
- `examples/two-node.yaml`, `examples/reports/` — a reference poc.yaml and a
  real e2e report tree.
