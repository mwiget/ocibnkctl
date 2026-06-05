# ocibnkctl

![BNK](https://img.shields.io/badge/BNK-2.3.0-0a3a5c)
![Kubernetes](https://img.shields.io/badge/Kubernetes-1.30.8-326ce5?logo=kubernetes&logoColor=white)
![k3s](https://img.shields.io/badge/k3s-v1.30.8-ffc61c)
![Go](https://img.shields.io/github/go-mod/go-version/mwiget/ocibnkctl)
![License](https://img.shields.io/github/license/mwiget/ocibnkctl)
![Last commit](https://img.shields.io/github/last-commit/mwiget/ocibnkctl)
[![Release](https://img.shields.io/github/v/release/mwiget/ocibnkctl?label=download)](https://github.com/mwiget/ocibnkctl/releases/latest)

Single-binary CLI that deploys F5 BIG-IP Next for Kubernetes (BNK) 2.3.0
on a two-node [k3s](https://k3s.io/) cluster — one combined
control-plane + worker (server), one worker (agent) dedicated to TMM
running in demo mode (virtio inside the pod netns; no DPU, no SR-IOV, no
Multus). The k3s nodes run directly as containers on the host OCI
runtime (docker or podman) — **no kind, no k3d, no third-party
orchestrator binary**.

Aimed at low-spec corporate laptops where dpubnkctl's bare-metal +
DPU pipeline is overkill. Same poc.yaml-driven, resume-safe shape;
much shorter pipeline.

## Demo

▶ **[Watch the ~3.5-minute demo](docs/video/ocibnkctl-demo.mp4)** — a real,
**live Claude Code session on a local model** drives `ocibnkctl` end to end:
scaffold the PoC → deploy F5 BIG-IP Next 2.3.0 → inspect every pod → diagnose a
stuck pod → run the scenario suite (12/12 green) → bnk-forge auto-registration
with live Traffic Flow → teardown. The whole production pipeline (headless
asciinema capture, Piper voiceover, playwright slides + bnk-forge UI shots,
ffmpeg assembly) is in [`docs/video/live-session/`](docs/video/live-session/).

## What this tool does

Drives a BNK deployment in three phases:

1. **cluster up** — start the k3s server + agent containers and join
   them, install Calico (acts as a simulator for larger SR-IOV
   deployments) in place of k3s's bundled flannel, label the worker for
   TMM, fetch kubeconfig.
2. **deploy prereqs** — namespaces, FAR pull secret, cert-manager.
3. **deploy flo + cne** — FLO from the release-manifest chart at
   `repo.f5.com`, License CR with the operator's JWT, CNEInstance with
   `advanced.demoMode.enabled: true` and TMM pinned via `nodeSelector:
   app=f5-tmm`.

Symmetric **`destroy`** unwinds it: bnk-forge unregister → remove the
k3s node containers → remove the cluster's docker network.

## Pinned versions

| Component | Version |
|---|---|
| BNK | 2.3.0 |
| CNE release manifest | 2.3.0-3.2598.3-0.0.170 |
| Kubernetes (k3s node image) | 1.30.8 (`rancher/k3s:v1.30.8-k3s1`) |
| Calico | v3.28.2 |
| cert-manager | v1.16.2 |
| FLO chart | resolved at deploy time from the release manifest |

## Minimum host resources

|                          | Cluster floor       | With bnk-forge      | Free disk |
|--------------------------|---------------------|---------------------|-----------|
| Linux (host docker)      | **12 cores · 24 GB**| **14 cores · 26 GB**| **~10 GB**|
| macOS / Windows Docker Desktop | **12 CPUs · 24 GB allocated to the VM** | **14 CPUs · 26 GB** | **~10 GB** |

(Configured in Docker Desktop → Settings → Resources. Rancher Desktop /
Colima use the same numbers — same underlying Linux VM model.)

### Why so much for a "demo"

TMM is pinned to the agent node via `nodeSelector: app=f5-tmm`. Unlike
kind, **k3s leaves the server node schedulable** (no control-plane
`NoSchedule` taint), so the remaining BNK pods spread across both nodes
rather than all piling onto one worker. The Kubernetes scheduler admits
pods against their `requests`, not their actual RSS, and the chart
reserves heavily:

> The per-pod values below are the BNK chart's request reservations
> (backend-independent), tabulated as a single-worker total. On the kind
> predecessor the tainted control-plane forced the whole stack onto one
> worker. On k3s the server node is schedulable, so the non-TMM load
> spreads across both nodes — measured ~13 pods on the server / ~7 on the
> agent in the validated run — easing per-node pressure. The host floor
> still applies, since one docker daemon backs both node containers.

| Pod (on the worker)                 | Memory request | CPU request |
|---|---|---|
| f5-tmm                              | 9204 Mi        | 4100m       |
| f5-cne-controller (4 containers)    | 1600 Mi        | 1080m       |
| f5-downloader                       | 1000 Mi        | 500m        |
| f5-spk-csrc                         | 1024 Mi        | 500m        |
| f5-crdconversion                    | 1024 Mi        | 500m        |
| f5-dssm-db / -sentinel              | 1152 Mi each   | 600m each   |
| f5-observer / -receiver             | 500 Mi each    | 512m / 1c   |
| f5-observer-operator                | 256 Mi         | 250m        |
| f5-spk-cwc                          | 640 Mi         | 556m        |
| f5-afm                              | 512 Mi         | 500m        |
| f5-ipam-ctlr / f5-rabbit            | 512 Mi each    | 100m / 300m |
| otel-collector / flo                | 256 Mi each    | 500m / 250m |
| **Sum on the worker**               | **~20 Gi**     | **~12 cores**|

Each k3s node container reports the docker daemon's full memory and
CPU as its allocatable — there is no partitioning. So the cluster won't
schedule the full stack until the daemon (or Docker Desktop VM) is
sized above the request total, plus headroom for the control-plane pods
and kernel overhead in both node containers.

### What the cluster actually uses

Steady-state, after `CNEInstance.Available=True`, the cluster's real
RSS is much smaller than the reservation:

| Component                        | Working set    | CPU       |
|---|---|---|
| TMM pod (worker)                 | ~1.2 GB        | ~100m     |
| kube-apiserver                   | ~900 MB        | ~150m     |
| All other F5 pods (~20)          | ~1.0 GB        | ~470m     |
| Calico + coredns + etcd + kube-* | ~700 MB        | ~150m     |
| Kernel / runtime per node        | ~500 MB × 2    | —         |
| **Total cluster RSS**            | **~4.5 GB pod + ~1 GB overhead** | **~900m steady, ~1.2c during TMM init** |

So the cluster lives inside ~6 GB of real memory once it's up — it
just won't *get there* without first satisfying the K8s scheduler's
~20 Gi worker reservation.

### Symptom when the floor is too low

`ocibnkctl e2e` reaches `[5/5] deploy-cne` and stalls. Six pods
(`f5-dssm-db-0`, `f5-dssm-sentinel-0`, `f5-spk-cwc-*`,
`f5-observer-{0,receiver-0,operator-*}`) sit `Pending` with
`FailedScheduling: Insufficient memory`, and
`CNEInstance.Available` never goes true. Quick check (nodes are
`k3s-<poc>-server-0` / `k3s-<poc>-agent-0`):

```bash
kubectl --kubeconfig <poc>/artifacts/kubeconfig describe node k3s-<poc>-agent-0 \
  | grep -E "Allocatable:|Allocated resources:" -A6
```

If `memory Requests` is ≥99% of `Allocatable`, raise the docker
daemon allocation and re-run from the failed phase (`ocibnkctl
deploy cne …`) — it's idempotent.

### Disk

~0.3 GB (`rancher/k3s` image) + ~2.4 GB (F5 container images pulled to
the worker) + ~0.5 GB (cert-manager, alpine/k8s tooling, manifests) +
~5 GB headroom for k3s cluster state and logs.

`ocibnkctl doctor` reports the host's actual CPU count and fails
when it falls below `MinBaseline`. Override the constants in
`internal/version/version.go` if you've tuned chart values to reduce
requests.

## bnk-forge integration

If a local [bnk-forge](https://github.com/sp-prod-field/bnk-forge)
clone exists at `~/git/bnk-forge` (or `$OCIBNKCTL_BNK_FORGE_PATH`)
when `ocibnkctl init` runs, the new PoC's `bnk_forge:` block is
pre-filled and enabled. On `cluster up`, ocibnkctl best-effort
registers the k3s cluster with bnk-forge — if the local bnk-forge
stack isn't running, the auto-hook logs a clean skip and continues.

**`ocibnkctl` never installs or starts bnk-forge for you.** If it's
configured but not running, bring it up manually (`cd ~/git/bnk-forge
&& make deploy`) then `ocibnkctl bnk-forge launch` to register
after the fact.

## Download

Prebuilt binaries for each tagged release are on the
[**GitHub Releases page**](https://github.com/mwiget/ocibnkctl/releases/latest) —
three archives per release plus a `checksums.txt`:

| Platform | Archive |
|---|---|
| Linux (Intel/AMD) | `ocibnkctl_<version>_linux_amd64.tar.gz` |
| Linux (ARM64) | `ocibnkctl_<version>_linux_arm64.tar.gz` |
| macOS (Apple Silicon) | `ocibnkctl_<version>_darwin_arm64.tar.gz` |

One-liner install (Linux amd64; swap the suffix for your platform):

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/mwiget/ocibnkctl/releases/latest | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
curl -fsSL "https://github.com/mwiget/ocibnkctl/releases/download/${VERSION}/ocibnkctl_${VERSION#v}_linux_amd64.tar.gz" \
  | tar -xz -C /tmp ocibnkctl
sudo install -m 0755 /tmp/ocibnkctl /usr/local/bin/ocibnkctl
ocibnkctl version
```

Releases follow `v<bnk-version>-<n>` — e.g. `v2.3.0-1`, `v2.3.0-2`.
The `2.3.0` prefix tracks the pinned BNK release; the `-n` suffix
increments per ocibnkctl-only iteration.

Or build from source — see [Repo layout](#repo-layout-the-binary-itself)
below.

## Requirements

| Tool | Why |
|---|---|
| **Docker** or **Podman** | runs the k3s nodes as containers; FLO + cert-gen also shell into an `alpine/k8s:1.31.5` container at deploy time. No separate cluster tool — the k3s nodes are launched via the runtime directly |
| **kubectl** | cluster reads/writes (apply, wait, label) |
| **helm** | cert-manager + FLO install, release-manifest pull |
| **git** *(optional)* | `init` git-inits the PoC repo (skippable with `--no-git`) |

Verify after install:

```bash
ocibnkctl doctor
```

`doctor` checks each tool (docker/podman, kubectl, helm) and the host
resource floor. For any that's **missing**, it prints a ready-to-run,
OS/arch-aware install command (and a docs link) right under the failed
check — so you can copy-paste the fix, or have an agent offer to run it.

What customers supply themselves, dropped into `keys/` of the PoC repo
(delivered through F5's normal channels):

- FAR tarball — image-pull credentials for `repo.f5.com`
- JWT — TEEM activation token

## Cluster backend (native k3s)

ocibnkctl ships a single backend: the k3s nodes (`rancher/k3s:v1.30.8-k3s1`)
run directly as containers on the host OCI runtime, driven through the
docker/podman CLI — there is **no third-party orchestrator binary** to
install. `cluster up` starts a server (combined control-plane + worker)
and an agent (the TMM worker), joins them over a per-cluster docker
bridge network, remounts each node's rootfs `rshared` (so Calico's
`mount-bpffs` init works), then layers Calico on top of k3s with its
bundled flannel/traefik/servicelb disabled. The result is the same
two-node, Calico-CNI, k8s-v1.30.8 shape the deploy pipeline expects.

Podman works through the same code path — set `cluster.provider: podman`
in `poc.yaml` (or let `doctor`/`cluster up` auto-detect the runtime).

## Quick start

```bash
# 1. Create a fresh PoC repo. Auto-detects ~/git/bnk-forge.
ocibnkctl init demo --customer "Acme"
cd demo

# 2. Drop the operator-supplied files into keys/.
cp /path/to/f5-far-auth-key.tgz keys/
cp /path/to/license.jwt          keys/.jwt

# 3. Confirm poc.yaml is clean.
ocibnkctl validate

# 4. Run the pipeline (~10–20 min with a warm docker cache).
ocibnkctl e2e --yolo --confirm-cluster demo

# 5. Tear down (symmetric):
ocibnkctl destroy --yolo --confirm-cluster demo
```

## Per-phase invocation

If you'd rather drive the phases one at a time for diagnostics:

```bash
ocibnkctl cluster up      --yolo --confirm-cluster demo
ocibnkctl deploy prereqs  --yolo --confirm-deploy  demo
ocibnkctl deploy flo      --yolo --confirm-deploy  demo
ocibnkctl deploy cne      --yolo --confirm-deploy  demo
```

Every phase is idempotent and gated by `--yolo` plus a typo-guard.

## Agentic workflow

You can drive a PoC conversationally with an AI coding agent instead of
typing the commands yourself — useful for getting your feet wet with BNK,
or for letting an agent deploy, inspect, and troubleshoot the cluster and
bnk-forge on your behalf.

Every PoC created by `ocibnkctl init` ships an **`AGENTS.md`** — an
operator + agent guide covering the pipeline, the `--yolo`/`--confirm-*`
safety gates, cluster inspection, the scenario workflow, bnk-forge, and
guardrails (read `poc.yaml` first, prefer `ocibnkctl` subcommands over
ad-hoc kubectl, confirm scope before destructive actions, treat `keys/`
as secret). A one-line **`CLAUDE.md`** `@`-includes it for Claude Code.

ocibnkctl does **not** embed an LLM — you bring your own agent and model
endpoint. The `agent` subcommand just prints the ready-to-paste
invocation for your preferred CLI, each pointed at the PoC's `AGENTS.md`:

```bash
ocibnkctl agent                 # list supported CLIs
ocibnkctl agent claude --poc ./demo   # print the invocation for Claude Code
```

```text
# Claude Code (https://docs.claude.com/en/docs/claude-code)
cd ./demo && \
  claude
# Then say:
#   "Read AGENTS.md, then walk me through deploying BNK on this PoC
#    (validate -> cluster up -> deploy), explaining each phase as you go."
```

Supported out of the box: `claude`, `gemini`, `aider`, `openai`,
`pi`, `opencode`. Set a custom model endpoint with `--llm-endpoint`
(or the CLI's own `ANTHROPIC_BASE_URL` / `OPENAI_API_BASE`). The agent
runs the same gated `ocibnkctl` subcommands you would — nothing
bypasses the `--confirm-*` typo-guards.

### The `AGENTS.md` guide

This is the operator + agent guide shipped verbatim into every PoC
(source: [`internal/embedded/files/AGENTS.md`](internal/embedded/files/AGENTS.md)
— edit it there, not here):

<details>
<summary>📖 <strong>AGENTS.md — ocibnkctl PoC operator + agent guide</strong></summary>

> You are driving a **PoC repo** created by `ocibnkctl init`. This file is
> your operating manual; `CLAUDE.md` in this directory simply `@`-includes
> it. Read `poc.yaml` first — it is the single source of truth for this
> deployment. Everything below is how to act on it safely.

#### What this PoC deploys

F5 BIG-IP Next for Kubernetes (BNK) 2.3.0 on a **two-node k3s cluster**:
one combined control-plane + worker (server) and one worker (agent)
dedicated to TMM. The k3s nodes run directly as containers on the host
OCI runtime (docker or podman) — there is **no kind, no k3d, no
third-party orchestrator binary**. TMM runs in **demo mode** (virtio
inside the pod netns); no DPU, no SR-IOV, no Multus on the base cluster.

#### Prerequisites (verify with `ocibnkctl doctor`)

- a container runtime: **docker or podman**
- **kubectl** and **helm** on PATH
- ~12 cores / ~24 GB free for the full BNK stack (see `doctor`)

No cluster tool to install — the k3s nodes are launched via the runtime
directly.

#### Customer-supplied secrets — required before any deploy

Drop these into `keys/` (gitignored) before running a deploy phase:

- `keys/f5-far-auth-key.tgz` — FAR image-pull credentials for repo.f5.com
- `keys/.jwt` — TEEM activation token

Both come from F5's normal license-portal channels. **Never commit them,
never echo their contents, never paste them into a chat or a report.**

#### The pipeline

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

**Safety gates (do not bypass without the operator's say-so).** Every
mutating phase requires **two** flags: `--yolo` (acknowledges the action
is destructive) and `--confirm-cluster <name>` (cluster mutations) **or**
`--confirm-deploy <name>` (in-cluster mutations) — the value must echo
`poc.yaml.metadata.name`, a typo-guard against acting on the wrong PoC.
`destroy` runs the pipeline in reverse (bnk-forge unregister → remove k3s
node containers + network).

#### PoC layout

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

#### Scenarios

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

#### bnk-forge (optional)

If `~/git/bnk-forge` (or `$OCIBNKCTL_BNK_FORGE_PATH`) exists when
`ocibnkctl init` runs, the `bnk_forge:` block is pre-filled and
`cluster up` best-effort registers the cluster with bnk-forge. If the
local stack isn't running, registration is skipped — deployment never
blocks on it. `ocibnkctl` will not install or start bnk-forge.

```
ocibnkctl bnk-forge launch       # ensure bnk-forge sees this cluster
ocibnkctl bnk-forge unregister   # remove it
```

#### How to act as an agent here

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

</details>

## Repo layout (the binary itself)

```
cmd/ocibnkctl/        main entrypoint
internal/cli/          cobra commands (init, validate, doctor, cluster,
                       deploy, destroy, e2e, bnk-forge, version)
internal/poc/          poc.yaml schema + I/O
internal/cluster/      native k3s backend + docker/podman wrappers
internal/deploy/       cert-manager, FLO, License CR, CWC cert-gen
internal/bnkforge/     bnk-forge HTTP client (copy-fork of dpubnkctl)
internal/embedded/     go:embed AGENTS.md, CLAUDE.md, templates/
internal/version/      build-stamped + BNK 2.3.0 pins + min-spec floor
```

## Repo layout (a PoC created by `ocibnkctl init`)

```
poc.yaml         declarative state — source of truth
AGENTS.md        operator + agent guide
CLAUDE.md        @AGENTS.md include
journal/         append-only markdown log
artifacts/       rendered k3s.yaml, kubeconfig, helm values, CWC certs
keys/            gitignored — FAR tgz + JWT live here
.gitignore       excludes all secret material
```

### Cluster access — the `~/.kube/config` lifecycle

`cluster up` installs this cluster's kubeconfig as **`~/.kube/config`** so
`kubectl` / `k9s` / etc. work without any `export`, and `destroy` puts
things back exactly as it found them:

```
cluster up ──┬─ ~/.kube/config absent  → create it
             └─ ~/.kube/config exists  → back up to ~/.kube/config.ocibnkctl-bak,
                                         then overwrite   (--yolo authorizes it)
      │
      ▼   kubectl · k9s · lens · … all work with no KUBECONFIG set
      │
destroy ─────┬─ we created it   → remove it
             └─ we overwrote it → restore your backup (and consume it)
```

The state of what was done is recorded in `artifacts/kube-global.json`, so
the revert is exact — your own config is never lost. Notes:

- Opt out at bring-up with `cluster up --skip-kubeconfig` — your
  `~/.kube/config` is then never touched.
- A PoC-scoped copy also lands at `artifacts/kubeconfig` (mode 0600); use
  it explicitly via `export KUBECONFIG=$(pwd)/artifacts/kubeconfig`.
  `ocibnkctl` itself always drives `kubectl`/`helm` through that path,
  independent of `~/.kube/config`.

### Browse the cluster with k9s

Because `cluster up` installs `~/.kube/config`, a terminal UI like
[k9s](https://k9scli.io/) works against the cluster with **zero config** —
just launch it and you're navigating the live BNK deployment: both k3s
nodes, the F5 control-plane pods, TMM and its 6 containers, logs, exec,
port-forwards, the lot.

```bash
# install k9s if needed — macOS: brew install k9s ; linux: see k9scli.io
ocibnkctl e2e --poc demo --yolo --confirm-cluster demo   # cluster + full BNK
k9s                       # picks up ~/.kube/config automatically — no export
```

![k9s navigating the BNK cluster on k3s](docs/k9s.jpg)

In k9s: `0` shows all namespaces, `/f5-` filters to the F5 pods, `l` tails
TMM's logs, `s` shells into a container.

## Network topology

The shape after a full `e2e` plus `bgp-peer-frr` (everything the
other scenarios build on) — the substance here (Calico, the Multus
NAD bridge, ZeBOS/BGP) is backend-agnostic. One docker bridge on the
host (the k3s cluster's own, `k3s-<poc>`); two k3s node containers
(`k3s-<poc>-server-0` = control-plane + worker, `k3s-<poc>-agent-0` =
TMM worker); a Multus-managed Linux bridge inside the worker carries
BGP traffic between TMM and the FRR helper pod. Scenario backends are
plain Calico pods — the Gateway IPs they serve get plumbed via BGP, so
the backends don't need to be on the NAD themselves. (Illustrative node
names below are shortened for the diagram.)

```
+----------------------------------------------------------------------------+
| HOST  (Linux or macOS Docker Desktop)                                      |
|                                                                            |
|   docker bridge: k3s   172.20.0.0/16                                       |
|       |                                                                    |
+-------|--------------------------------------------------------------------+
        |
+-------+--------------+   +-------------------------------------------------+
| smoke-control-plane  |   | smoke-worker  (k3s node container)              |
| (k3s node container) |   | label: app=f5-tmm                               |
| eth0 172.20.0.2      |   | eth0 172.20.0.3                                 |
|                      |   |                                                 |
| pods:                |   |  +-------------------------------------------+  |
|   Calico  Multus     |   |  | TMM pod        ns=default  app=f5-tmm     |  |
|   FLO     CWC        |   |  | 6 containers:                             |  |
|   cert-manager       |   |  |   f5-tmm                                  |  |
|   ...                |   |  |   f5-tmm-routing  (= ZeBOS)               |  |
+----------------------+   |  |   debug  blobd  toda-observer  ipsec      |  |
                           |  | Interfaces:                               |  |
                           |  |   net1   192.168.99.X/24  Multus NAD      |  |
                           |  |          (BGP source, no eth0 hook)       |  |
                           |  |   eth0   10.244.x.x/32   Calico (kube-API |  |
                           |  |          + ZeBOS bgpd kernel listener)    |  |
                           |  |   xeth0  no IP    Calico veth #2, TMM     |  |
                           |  |          userspace raw frames             |  |
                           |  |   tmm    169.254.0.253/24  virtio, pod    |  |
                           |  |          default route to TMM DP          |  |
                           |  |   tunl0  DOWN     Calico IPIP placeholder |  |
                           |  +-------------------------------------------+  |
                           |  +-------------------------------------------+  |
                           |  | FRR pod        ns=scn-bgp  app=scn-frr    |  |
                           |  | 1 container:   frr (zebra + bgpd)         |  |
                           |  |   net1   192.168.99.Y/24  Multus NAD      |  |
                           |  |          (BGP peer + curl source)         |  |
                           |  |   eth0   10.244.x.x/32   Calico           |  |
                           |  +-------------------------------------------+  |
                           |             ^                                   |
                           |             |  BGP TCP/179 + scenario curls     |
                           |             v  over br-bnk-bgp, L2              |
                           |  +========================================+     |
                           |  ||  br-bnk-bgp   Linux bridge in node    ||    |
                           |  ||  netns, created by the bridge-CNI     ||    |
                           |  ||  plugin via NAD name=bnk-bgp ;        ||    |
                           |  ||  host-local IPAM 192.168.99.20-250    ||    |
                           |  ||  on /24                               ||    |
                           |  +========================================+     |
                           |                                                 |
                           |  +-------------------------------------------+  |
                           |  | scenario backends  (plain Calico pods —   |  |
                           |  | no NAD attachment, no node pinning)       |  |
                           |  |   nginx        ns=scn-httproute-e2e       |  |
                           |  |   pp-backend   ns=scn-proxy               |  |
                           |  |   ext-backend  ns=scn-extres   (Pool      |  |
                           |  |     member references its Calico podIP)   |  |
                           |  +-------------------------------------------+  |
                           |                                                 |
                           |  DaemonSets in node netns:                      |
                           |    Calico-node     Multus thick                 |
                           |    f5-coremond (if how-to #4 ran)               |
                           +-------------------------------------------------+

BGP session:
  TMM/ZeBOS  AS 65000  =======  net1 <-> net1, L2 over br-bnk-bgp  =======>  FRR  AS 65001
                                                                             listen-range
                                                                             192.168.99.0/24
                                                                             peer-group
                                                                             from-tmm

  TMM ZeBOS advertises (redistribute kernel, at router-bgp scope —
  silently dropped if placed inside address-family ipv4):
    192.168.99.0/24      (net1 connected)
    203.0.113.100/32     Gateway scn-gateway        (http-routing-e2e)
    203.0.113.101/32     Gateway scn-extres-gw      (external-resource-pool)
    203.0.113.102/32     Gateway scn-proxy-gw       (proxy-protocol-l4)

  FRR installs each /32 as a kernel route:
    203.0.113.100/32 via 192.168.99.X dev net1 proto bgp
  so any client in the FRR pod can curl the Gateway addresses
  end-to-end via the NAD bridge, completely bypassing TMM's eth0
  TCP hook. This is what http-routing-e2e and external-resource-pool
  rely on for their data-plane assertions.
```

Key knob: `CNEInstance.spec.advanced.tmm.env TMM_MAPRES_ADDL_VETHS_ON_DP=FALSE`
is set by `bgp-peer-frr`. With this `TRUE` (TMM's default for
demoMode), `mapres` grabs `net1` for the userspace data plane and
flushes its kernel IP — ZeBOS then has nothing to source-bind
to. Flipping it `FALSE` lets `net1` stay a normal Linux interface
with its NAD-assigned IP so the kernel TCP stack handles BGP
traffic ordinarily.

## Scenarios — testing F5 how-tos against the running cluster

After `e2e` brings the cluster up, drive named test scenarios against
it. Each scenario maps to one F5 how-to article (or sub-article) and
exercises a slice of BNK functionality end-to-end: render manifests
into `artifacts/scenarios/<name>/`, apply them, assert reconciled
state, write a JSON+md report under `reports/<timestamp>/scenarios/`.

> **Validated on native k3s.** A clean run — fresh cluster → `e2e`
> deploy → `scenario run --all` — passes **12/12 green, 0 failed** on the
> k3s backend (measured 2026-06-04, full parity with the kind
> predecessor). The ratings below hold as measured; the wall times are
> indicative — the authoritative current timings are in the checked-in
> [reference report](#reference-run-report). Getting there took four
> k3s-specific fixes (a `standard` StorageClass, a `/var/run`→`/run`
> symlink for Multus netns, host-side `docker cp` of the bridge CNI, and
> arch-aware plugin selection) — all in `cluster up` / the scenarios now.

```bash
ocibnkctl scenario list                            # all known scenarios + rating
ocibnkctl scenario run http-routing --poc ./demo   # apply + verify + report
ocibnkctl scenario run http-routing --dry-run      # render manifests only
ocibnkctl scenario clean http-routing              # delete what was applied
```

Rating is a stable hint about what's testable in the 2-node / demo-TMM
shape:

| Rating | Meaning |
|---|---|
| green | fully testable here |
| amber | partially testable — control-plane verifies, data-plane plumbing missing |
| red   | requires real DPUs / BGP peers / etc.; listed for discoverability, never executed |

Scoring of the [F5 BNK how-tos index](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/):

| # | How-to | Rating | Scenario | Wall time |
|---|---|---|---|---|
| 1 | [Restrict access to sensitive data](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/spk-admin-access-api.html) | 🟢 | [`cwc-admin-access`](internal/scenarios/cwcadminaccess) | 9s |
| 2 | [Components needing cluster-wide access](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/spk-whole-cluster.html) | 🟢 | [`cluster-wide-watch`](internal/scenarios/clusterwidewatch) | 4s |
| 3 | [Set up dynamic routing with BGP](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/spk-zebos-config.html) | 🟢 | [`bgp-peer-frr`](internal/scenarios/bgppeer) | 3m19s |
| 4 | [Set up core file collection](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/spk-coremond.html) | 🟢 | [`core-file-collection`](internal/scenarios/corefiles) | 3m01s |
| 6 | [Configure Token Counting and Enforcement](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-token-counting-and-enforcement.html) | 🟢 | [`ai-token-counting`](internal/scenarios/aitokencount) | 25s |
| 7 | [Semantic AI Model Caching](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/ai-related-features/ai-semantic-caching.html) (sub-article of [AI Traffic Optimization](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/ai-related-features/index.html)) | 🟢 | [`ai-semantic-cache`](internal/scenarios/aisemcache) | 22s |
| 8 | [HTTP traffic steering with Gateway API HTTPRoute](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/Configure-HTTP-traffic-steering-with-Gateway-API-HTTPRoute.html) | 🟢 | [`http-routing-e2e`](internal/scenarios/httproutee2e) | 21s |
| 9 | [Proxy Protocol iRule support for L4 routes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html) | 🟢 | [`proxy-protocol-l4`](internal/scenarios/proxyprotocol) | 24s |
| 10 | [Load Balance Traffic to External Resources](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html) | 🟢 | [`external-resource-pool`](internal/scenarios/extrespool) | 14s |

Plus four scenarios drawn from the BNK Use-Cases / CRD pages rather
than the how-tos index:

| Use-case | Rating | Scenario |
|---|---|---|
| [Dynamic IP address allocation (FIC for Gateway API)](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/use-cases/bnk-ficforgatewayapi.html) | 🟡 | [`fic-dynamic-ip`](internal/scenarios/ficdynamicip) |
| TCP load balancer ([`L4Route`](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-l4route.html) protocol=TCP, weighted backends) | 🟢 | [`tcp-l4-loadbalance`](internal/scenarios/tcpl4lb) |
| UDP load balancer ([`L4Route`](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-l4route.html) protocol=UDP, socat echo) | 🟢 | [`udp-l4-loadbalance`](internal/scenarios/udpl4lb) |
| gRPC routing ([`GRPCRoute`](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-grpcroute.html), grpcbin backend, grpcurl client) | 🟡 | [`grpc-loadbalance`](internal/scenarios/grpcroute) |

`fic-dynamic-ip` (🟡): manifest-side configuration applies cleanly
(F5BnkGateway, Gateway w/ infrastructure.parametersRef, HTTPRoute)
but Gateway never reaches Programmed=True. f5-cne-controller logs
"No IPAM found for Gateway" — the F5BnkGateway pool isn't auto-
converted into IPAM/IPAMRange CRs in this BNK 2.3.0 demo
deployment. The scenario asserts the control-plane state and
surfaces the AddressNotAssigned condition as informational.

`grpc-loadbalance` (🟡): GRPCRoute reconciles, Gateway reaches
Programmed=True, BGP route propagates, grpcurl installs SHA-
verified, and a direct grpcurl-to-backend Service call lists
all gRPC services successfully. But cleartext gRPC traffic
through the Gateway returns RST_STREAM(INTERNAL_ERROR) — TMM's
standard HTTP/json/httprouter profile chain (visible in audit
logs) breaks gRPC framing. Investigation confirmed TMM
unconditionally applies `profile-http` + `profile-json` +
`profile-httprouter` to all listener types (HTTP and HTTPS),
corrupting HTTP/2 binary frames regardless of TLS termination.
Setting `appProtocol: kubernetes.io/h2c` on the Service, switching
to an HTTPS listener on port 443 with TLS, and adding `profile-sbi`
(all verified via TMM audit logs) did not change the outcome. This
is a BNK 2.3.0 FLO limitation. Fix needs either a "raw HTTP/2
passthrough" mode for GRPCRoute listeners, or a BNK profile
override path not yet exposed through the Gateway API CRDs.

Wall times measured on a fresh `e2e` (cluster destroy + redeploy)
running 2026-05-21 on a Linux laptop. The two TMM-restarting
scenarios (`bgp-peer-frr` + `core-file-collection`) dominate at
~3 minutes each; the others are tens of seconds because they
either don't touch TMM or piggyback on the bridge already up.
`ocibnkctl scenario run --all` runs every green-rated scenario
in topo-sorted dependency order, writing an aggregate
`reports/<stamp>/run.{json,md}` summary alongside the per-scenario
JSONs.

Cluster bring-up itself (`ocibnkctl e2e`) is **~5m10s**:
validate 0s · cluster-up 31s · deploy-prereqs 20s · deploy-flo
23s · deploy-cne 3m56s (includes waiting on `bnk-gatewayclass`
to reach `Accepted=True` — required to keep first-run scenario
Gateways from being marked-for-deletion by the controller).

How-tos **#5** ([DOCA Offloads on DPU](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/traffic-offload.html)),
**#11** ([Static Active-Standby Interface Bonding](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-static-active-standby-bonding.html)),
and **#12** ([TMOS DNS Service Integration with CIS](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-tmos-dns-service-integration-with-container-ingress-services.html))
are omitted from the table because they require resources this
shape can't provide: DPU silicon (#5), bondable physical NICs (#11),
and a real upstream BIG-IP GTM box (#12). They remain valid BNK
features outside the ocibnkctl shape.

Ratings are assigned only after a scenario is built and run.
Implemented scenarios that pan out land as 🟢; ones that
hit a real architectural barrier on k3s+demoMode get 🟡
with the gap documented in the scenario's `Description()`.
Empty cell = scenario not yet built.

`bgp-peer-frr` (green) deploys a real BGP session between an FRR
pod and TMM's ZeBOS daemon, peered over a Multus
NetworkAttachmentDefinition (bridge CNI) on a per-node Linux
bridge. The NAD path bypasses TMM's eth0 TCP hook entirely —
BGP rides net1 in both pods, exchanging prefixes via the bridge.
Six assertions pass: Multus DaemonSet Ready, both pods have net1
in the 192.168.99.0/24 NAD range, ZeBOS sees the neighbor, BGP
session Established, and FRR's BGP table has at least one prefix
learned from TMM (via `redistribute kernel`).

`http-routing-e2e` (green) — depends on `bgp-peer-frr` for the
NAD plumbing. Applies a GatewayClass + Gateway (static
spec.addresses=203.0.113.100) + HTTPRoute + nginx backend.
TMM's ZeBOS (via `redistribute kernel`) advertises 203.0.113.100/32
into BGP; FRR installs the kernel route via net1; the verify
step execs 5 curls from inside the FRR pod, which already has
the route. All 6 assertions pass including the 5×curl. Path:
FRR socket → FRR kernel route → net1 → bnk-bgp bridge → TMM net1
→ Gateway listener → nginx. TMM's eth0 TCP hook is completely
bypassed.

Reproduce manually:

```bash
kubectl -n scn-bgp exec deploy/scn-frr -c frr -- \
  curl -sS -H 'Host: ocibnkctl.local' http://203.0.113.100/
# → ocibnkctl-scenario-httproute-e2e-OK
```

`external-resource-pool` (green) — demonstrates how-to #10 (load
balance to non-Service backends) via the BNK `Pool` CR. HTTPRoute
`backendRefs` points at a `Pool {group:k8s.f5net.com, kind:Pool}`
instead of a Service; `Pool.spec.members` lists endpoints by
IP+port. In this shape, the "external" backend is an nginx pod attached
to the bnk-bgp NAD (same bridge TMM uses), with its NAD IP
auto-discovered and rendered into the Pool CR. Gateway address
is 203.0.113.101 to avoid collision with `http-routing-e2e`.

`cwc-admin-access` (green) — implements how-to #1 (restrict access
to sensitive data). Demonstrates BNK's dual-gate access control
on the CWC admin API: mTLS at the TLS layer + bearer token at
the HTTP layer. Both materials are produced by the deploy-flo
phase already (cwc-license-client-certs Secret + cwc-auth-token
Secret in f5-cne-core); the scenario just replicates them into
its own namespace, spawns a curl probe pod, and runs three
requests against https://f5-spk-cwc.f5-cne-core.svc:38081/status:
authenticated (expect 200 + license JSON), no Authorization
header (expect 401 "invalid token format"), bogus token
(expect 401 "invalid token"). Independent of bgp-peer-frr —
this is a pure runtime-access check.

`proxy-protocol-l4` (green) — implements how-to #9 (PROXY-protocol
iRule on an L4 route). The new BNK CRs reconcile (`F5BigCneIrule`
Programmed, `L4Route` Accepted, `BNKNetPolicy` ResolvedRefs True),
TMM proxies the TCP traffic, FRR learns the Gateway IP via BGP,
and 5/5 curls from FRR through the Gateway return the marker body
with the parsed `proxy_addr` set to FRR's NAD IP — proving the
iRule's `TCP::respond` prepended the PROXY v1 line before nginx
saw the request. Load-bearing knob: `L4Route.spec.pvaAccelerationMode:
disabled`, which keeps the data path in TMM's TCL slow path. With
the default `full/assisted` PVA mode, TMM hardware-offloads the
connection after handshake and `TCP::respond` fires in the VM but
can't reach the offloaded wire — symptoms are 200 OK from nginx
turning into "broken header" errors and curl `(52) Empty reply`.

`core-file-collection` (green) — implements how-to #4 (set up
core file collection). One-line CNEInstance.spec.coreCollection.
enabled=true flip plus `advanced.coremon.hostPath=true` so the
CoreMond DaemonSet survives the single-node-RWO storage class.
FLO auto-creates a CoreMond CR + DaemonSet in f5-cne-core and
adds kernel-cores / f5-core-store / tmm-core volumes to the TMM
Deployment template. The scenario asserts the CR exists, the
DaemonSet is Running, and the CNEInstance condition
`CoremondAvailable=True`. The how-to's "kill -11 to force a
crash" verification step is intentionally NOT automated —
crashing TMM mid-scenario destabilises the cluster, and the
follow-up "did a core file land in /var/crash" check needs a
privileged node-level read we'd rather not bake in. Operators
can run the kill manually after the scenario and inspect the
k3s worker container's filesystem to confirm capture.

## Reference run report

A complete `e2e --with-scenarios` report from a clean cluster
is checked in at
[`examples/reports/run-lab-2026-06-04T16-45-09Z.md`](examples/reports/run-lab-2026-06-04T16-45-09Z.md)
so a reader can see the full report shape (versions, host
resources, cluster topology, F5 control-plane pods, every deploy
phase, and every scenario row) without running anything locally.

> This is a **native-k3s** run (`e2e --with-scenarios --no-resume` from a
> fresh cluster on 2026-06-04): **17 ok, 0 failed** — deploy 5/5 +
> scenarios 12/12 green.

Reproduce on your own host with:

```bash
ocibnkctl e2e \
  --poc <pocdir> \
  --yolo --confirm-cluster <pocname> \
  --with-scenarios \
  --no-resume
```

Output lands at `<pocdir>/reports/<stamp>/run-<pocname>-<stamp>.md`
(plus the JSON twin, per-phase logs under `logs/`, and
per-scenario JSONs under `scenarios/`).
The checked-in report ran 15m4s end-to-end: ~5m deploy
(validate → cluster-up → deploy-prereqs/flo/cne) plus 9m47s
running 12 green scenarios topo-sorted by dependency order
(the one amber scenario — `fic-dynamic-ip` — is skipped by
`--all` and must be run explicitly).

## Testing

```bash
make test    # Go unit tests (poc, deploy, cluster, scenarios)
make smoke   # unit tests + Layer A CLI smoke (no cluster required, ~5s)
```

`make smoke` is the gate to run before pushing — it covers the
non-cluster-dependent surface area in one shot.

## Design references

- **[dpubnkctl](https://github.com/mwiget/dpubnkctl)** — the
  bare-metal + DPU sister tool. ocibnkctl is a copy-fork:
  `internal/poc`, `internal/cluster`, `internal/cli` are rewritten
  for the k3s path; `internal/bnkforge`, `internal/deploy` are
  forked verbatim with minor adjustments (local kubectl/helm
  instead of containerized).
- **[f5-bnk-udf](https://github.com/f5devcentral/f5-bnk-udf/tree/v2.2.0)**
  (branch `v2.2.0`) — the inspiration for the BNK-on-host shape:
  `advanced.demoMode.enabled: true` + node label + nodeSelector,
  ZeBOS dynamic-routing ConfigMap pattern, multi-worker
  topology. Same CNEInstance recipe family; ocibnkctl adapts it
  to a two-node k3s cluster with Multus NADs replacing the
  macvlan-on-bare-metal approach used in f5-bnk-udf.
