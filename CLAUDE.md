# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this binary is

`ocibnkctl` is a single-binary Go CLI that drives a full F5 BIG-IP Next for
Kubernetes (BNK) 2.3.0 deployment onto a two-node k3s cluster — one combined
control-plane+worker (server), one worker (agent) dedicated to TMM running in
**demo mode** (virtio inside the pod netns, no DPU/SR-IOV/Multus required for
the base shape). It is a copy-fork of
[`dpubnkctl`](https://github.com/mwiget/dpubnkctl) with the
bare-metal/DPU/kubespray pipeline replaced by native k3s-in-containers, but
with `internal/deploy` and `internal/bnkforge` forked verbatim (with local
kubectl/helm instead of containerized). It is itself the successor to
`kindbnkctl` (the kind/k3d-backed predecessor).

Versions are pinned in `internal/version/version.go` and stamped into the
binary via `-ldflags` (see Makefile).

**Cluster backend.** A single native **k3s** backend (`internal/cluster/k3s.go`,
implementing the `Provisioner` interface). It runs `rancher/k3s` server +
agent containers directly on the host OCI runtime (docker or podman) via the
runtime CLI — **no kind, no k3d, no third-party orchestrator binary**.
`CreateCluster` starts the server, remounts each node's rootfs `rshared` (so
Calico's `mount-bpffs` init works — plain `docker run` is `rprivate`), joins
the agent over a per-cluster bridge network with a shared token, and extracts
the kubeconfig via `docker exec`, rewriting it to the host-mapped API port.
k3s's bundled flannel/traefik/servicelb are disabled so Calico is the CNI;
the result is the same two-node Calico-CNI v1.30.8 shape the deploy pipeline
expects. Note k3s leaves the server node **schedulable** (no control-plane
taint, unlike kind), so non-TMM pods spread across both nodes.

## Build / test / run

```bash
make build               # darwin/amd64 host build → bin/ocibnkctl
make build-all           # host + linux-arm64
make release             # versioned + sha256 release artifacts for linux-amd64 + darwin-arm64
make install             # → ~/.local/bin/ocibnkctl
make test                # go test ./...
make smoke               # unit tests + Layer-A CLI smoke (no cluster, ~5s) — the gate before pushing
make fmt vet tidy
```

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
validate → cluster up → deploy prereqs → deploy flo → deploy cne
```

Each phase is idempotent and gated by `--yolo` plus a typo-guard:
`--confirm-cluster <name>` (cluster mutations) or `--confirm-deploy <name>`
(in-cluster mutations) must echo the PoC name. `e2e` chains all five phases.
`destroy` runs them in reverse: bnk-forge unregister → remove k3s node containers
→ remove the cluster's docker network.

The deploy phase composes three things: cert-manager via helm, the FLO chart
pulled at deploy time from the BNK release manifest at `repo.f5.com`, and a
`CNEInstance` CR with `advanced.demoMode.enabled: true` and TMM pinned via
`nodeSelector: app=f5-tmm`. The CWC cert-gen step shells into an
`alpine/k8s:1.31.5` container — that container image is a hard runtime
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
internal/embedded/     go:embed of AGENTS.md, CLAUDE.md, templates/
internal/version/      build-stamped version + BNK 2.3.0 pins + min-spec floor
```

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

Ratings — set by the scenario itself, only after it's been run:

| Rating | Meaning |
|---|---|
| **green** | fully testable in the 2-node demo-TMM shape |
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
(separate repo, `../regcachectl`) runs a host-local pull-through cache fleet —
anonymous `registry:2` caches for `docker.io`/`ghcr.io`/`quay.io` and a
**credential-free blob cache** for `repo.f5.com`. Opt a cluster in via `poc.yaml`:

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
