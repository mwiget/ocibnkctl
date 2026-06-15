# AGENTS.md — ocibnkctl PoC operator + agent guide

> You are driving a **PoC repo** created by `ocibnkctl init`. This file is
> your operating manual; `CLAUDE.md` in this directory simply `@`-includes
> it. Read `poc.yaml` first — it is the single source of truth for this
> deployment. Everything below is how to act on it safely.

## What this PoC deploys

F5 BIG-IP Next for Kubernetes (BNK) 2.3.0 on a **k3s cluster**: one
combined control-plane + worker (server) and **`cluster.tmm_nodes`**
worker (agent) node(s) dedicated to TMM (default 1). The k3s nodes run
directly as containers on the host OCI runtime (docker or podman) —
there is **no kind, no k3d, no third-party orchestrator binary**. TMM
runs in **demo mode** (virtio inside the pod netns); no DPU, no SR-IOV,
no Multus on the base cluster.

**Scaling TMM.** Set `cluster.tmm_nodes: N` before deploy, or scale a
live cluster with `ocibnkctl scale --tmm N --yolo --confirm-cluster
<name>` (joins/removes labelled agent nodes and adjusts
`CNEInstance.tmmReplicas`, one TMM per node). Optionally pick an
all-active data plane with `bnk.tmm_dataplane_mode`:

- `selfip-dag` — every TMM owns a self-IP and is active (deploy installs
  Multus + a bridge NAD + an F5SPKVlan with per-TMM self-IPs and a
  pod_hash DAG). No upstream router needed. `bnk.tmm_active_active: true`
  is the legacy alias for this.
- `anycast-bgp` — every TMM runs mapres `FALSE` and advertises the same
  VIP `/32` over its own ZeBOS/BGP session to a co-located FRR peer
  (deploy installs Multus + the `bnk-bgp` NAD + an FRR DaemonSet on the
  TMM nodes). Models anycast/ECMP.

NOTE: on a single host the per-node bridges are isolated, so neither mode
fans one VIP's throughput across nodes — each TMM serves the traffic that
lands on its own node (selfip-dag), and each FRR sees only its node-local
TMM (anycast-bgp). Real cross-node fan-out needs DPU/SR-IOV or a
shared-L2 underlay + upstream ToR, out of scope for demo mode.

## Prerequisites (verify with `ocibnkctl doctor`)

- a container runtime: **docker or podman**
- **kubectl** and **helm** on PATH
- ~12 cores / ~24 GB free for the full BNK stack (see `doctor`)

No cluster tool to install — the k3s nodes are launched via the runtime
directly.

## Customer-supplied secrets — required before any deploy

Drop these into `keys/` (gitignored) before running a deploy phase:

- `keys/f5-far-auth-key.tgz` — FAR image-pull credentials for repo.f5.com
- `keys/.jwt` — TEEM activation token

Both come from F5's normal license-portal channels. **Never commit them,
never echo their contents, never paste them into a chat or a report.**

## The pipeline

```
validate  →  cluster up  →  deploy prereqs  →  deploy flo  →  deploy cne
```

Each phase is idempotent and resume-safe. Run them individually, or
chain all five with one command:

```
ocibnkctl e2e --yolo --confirm-cluster <poc-name>
```

A full run takes ~10–20 min on a laptop with a warm image cache. Per-phase:

```
ocibnkctl validate
ocibnkctl cluster up     --yolo --confirm-cluster <poc-name>
ocibnkctl deploy prereqs --yolo --confirm-deploy <poc-name>
ocibnkctl deploy flo     --yolo --confirm-deploy <poc-name>
ocibnkctl deploy cne     --yolo --confirm-deploy <poc-name>
```

### Safety gates (do not bypass without the operator's say-so)

Every mutating phase requires **two** flags:

- `--yolo` — acknowledges the action is destructive
- `--confirm-cluster <name>` (cluster mutations) **or**
  `--confirm-deploy <name>` (in-cluster mutations) — the value must echo
  `poc.yaml.metadata.name`. This is a typo-guard against acting on the
  wrong PoC.

Before running anything destructive, confirm the PoC name and scope with
the operator. `destroy` runs the pipeline in reverse (bnk-forge
unregister → remove k3s node containers + network).

## PoC layout

```
poc.yaml         source of truth — tear-down + redeploy read only this
AGENTS.md        this guide          CLAUDE.md  @AGENTS.md include
journal/         append-only markdown log written during runs
artifacts/       rendered k3s.yaml, kubeconfig (0600), helm values, certs
keys/            gitignored — FAR tgz + JWT live here
```

Inspect the running cluster. `cluster up` installs this cluster's
kubeconfig as `~/.kube/config` by default (backing up and overwriting any
existing one — `--yolo` authorizes it), so `kubectl` / `k9s` work directly:

```
kubectl get nodes          # k3s-<name>-server-0, k3s-<name>-agent-0
kubectl get pods -A
```

A PoC-scoped copy also lives at `artifacts/kubeconfig` (use it via
`export KUBECONFIG=$(pwd)/artifacts/kubeconfig`). `destroy` reverts
`~/.kube/config`; opt out at bring-up with `--skip-kubeconfig`.

## Scenarios

After a successful deploy, exercise BNK features. Each scenario maps to
an F5 how-to article, renders manifests, applies them, asserts state,
and writes a JSON+md report under `reports/<timestamp>/`.

```
ocibnkctl scenario list            # names + ratings (green/amber/red)
ocibnkctl scenario run --all       # all green scenarios
ocibnkctl scenario run <name>      # one scenario
ocibnkctl scenario clean <name>    # delete what a scenario applied
```

Ratings: **green** = fully testable in this demo shape; **amber** =
control-plane verifies but data-plane plumbing is partially missing;
**red** = needs DPUs / real upstream BIG-IP (never executed here). Many
scenarios depend on `bgp-peer-frr`, which installs Multus + an FRR BGP
peer on demand (the base cluster has no Multus).

## bnk-forge (optional)

If `~/git/bnk-forge` (or `$OCIBNKCTL_BNK_FORGE_PATH`) exists when
`ocibnkctl init` runs, the `bnk_forge:` block is pre-filled and
`cluster up` best-effort registers the cluster with bnk-forge. If the
local stack isn't running, registration is skipped — deployment never
blocks on it. `ocibnkctl` will not install or start bnk-forge.

```
ocibnkctl bnk-forge launch       # ensure bnk-forge sees this cluster
ocibnkctl bnk-forge unregister   # remove it
```

## How to act as an agent here

- **Read `poc.yaml` and the latest `journal/` entry first** to learn the
  current state before proposing any action.
- **Prefer `ocibnkctl` subcommands over ad-hoc kubectl/helm/docker.** The
  CLI is idempotent and journals what it does; raw commands drift from
  the source of truth. Before writing a new script for something, check
  whether a subcommand or flag already does it (`ocibnkctl --help`,
  `<cmd> --help`).
- **Confirm scope before destructive actions** and never invent or
  auto-fill the `--confirm-*` gate without the operator agreeing.
- **Treat `keys/` as secret.** Never read, print, commit, or transmit its
  contents. Reports are scrubbed of secrets before sharing.
- **Surface failures honestly** — if a phase fails, show the real output
  and the failing step; don't paper over it.
- When stuck, `ocibnkctl doctor` and the per-phase logs under
  `artifacts/` are the fastest way to see what the environment actually
  reports.
- **If `doctor` reports a missing host tool** (docker/podman, kubectl,
  helm) it prints a ready-to-run, OS-aware install command. Offer to run
  it for the operator — but confirm first, since installing host tools is
  a system change.
