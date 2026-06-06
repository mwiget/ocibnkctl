# Raspberry Pi 5 — full e2e performance

A complete `ocibnkctl e2e --with-scenarios` run on a single 4-core Raspberry
Pi 5, from an empty host through a deployed BIG-IP Next for Kubernetes 2.3.0
cluster and all 12 green how-to scenarios. This is the tight-host envelope —
the small-host (`host_profile: small`) profile plus the automatic
`deploy-shrink` phase — so it is the slowest, most constrained shape the tool
targets, not a roomy reference machine.

## Host

| Field | Value |
|---|---|
| Board | Raspberry Pi 5 Model B Rev 1.1 |
| CPU | 4 cores (arm64 / aarch64) |
| Memory | 15.6 GB |
| OS | Ubuntu 24.04.4 LTS |
| Kernel | 6.8.0-raspi |
| Container runtime | Docker 29.5.3 |
| Kubernetes | k3s v1.30.8+k3s1 (2 nodes: server + agent) |

## Configuration

- `bnk.host_profile: small` — CNEInstance renders with the metrics subsystem
  off, dropping TMM from 4.1c → 3.4c so it fits a single 4-core node.
- **Auto `deploy-shrink`** — engaged automatically (host < the 10-core
  standard floor): Kyverno caps every F5 + kube-system pod's CPU/memory
  requests so the footprint fits.
- **bnk-forge** enabled and registered during `cluster up`.
- Native arm64 throughout — all F5 and third-party images run without
  emulation.

## Run

`ocibnkctl e2e --poc <poc> --yolo --confirm-cluster <poc> --no-resume --with-scenarios`

- **Build:** ocibnkctl `9c0592e` · BNK 2.3.0 · CNE manifest 2.3.0-3.2598.3-0.0.170
- **Result:** deploy **6/6 ok** · scenarios **12/12 green, 0 failed**
- **Timing:** deploy from an empty host in **8m41s** (cold, fresh `destroy`
  first); the all-green scenario sweep in **7m54s** (`scenario run --all` on
  the now-warm cluster — the figures below).

### Deploy phases — 8m41s

| # | Phase | Status | Time |
|---|---|---|---|
| 1 | validate | ok | 0s |
| 2 | cluster-up | ok | 1m2s |
| 3 | deploy-prereqs | ok | 27s |
| 4 | deploy-flo | ok | 32s |
| 5 | deploy-shrink *(auto)* | ok | 32s |
| 6 | deploy-cne | ok | 6m8s |

`deploy-cne` dominates: it waits for the TMM pod, the license to go Active,
and the GatewayClass to reconcile — all on a CPU-saturated 4-core host.

### Scenarios — 12/12 green, 7m54s

`ocibnkctl scenario run --all` (dependency-ordered) against the deployed cluster:

| Scenario | Status | Time |
|---|---|---|
| bgp-peer-frr | ok | 3m6s |
| ai-semantic-cache | ok | 9s |
| ai-token-counting | ok | 9s |
| cluster-wide-watch | ok | 6s |
| core-file-collection | ok | 2m52s |
| cwc-admin-access | ok | 5s |
| external-resource-pool | ok | 11s |
| grpc-loadbalance | ok | 12s |
| http-routing-e2e | ok | 10s |
| proxy-protocol-l4 | ok | 10s |
| tcp-l4-loadbalance | ok | 15s |
| udp-l4-loadbalance | ok | 23s |

`bgp-peer-frr` and `core-file-collection` are the long poles — both restart
the TMM pod (Multus NAD attach / coreCollection toggle) and wait for the
rollout, which is slow on a 4-core node. The rest land in seconds once the
backends and Gateways reconcile.

## Notes

1. **cwc-admin-access** is timing-sensitive on a tight host: when it runs
   right after another scenario restarts TMM, the CWC admin API can briefly
   serve a not-yet-`Active` license. Its authenticated `/status` check now
   retries (up to 90s) until the populated 200 lands, so it passes reliably
   rather than racing the rollout. The reject checks stay single-shot.

2. This shape exercises the Pi-specific fixes end-to-end: the node-container
   **DNS pin** (real upstream resolvers via `--dns`, so image pulls and
   CoreDNS don't depend on the flaky embedded-DNS-to-loopback proxy), the
   **auto `deploy-shrink`** phase, and arm64 image selection across all
   scenarios (e.g. `kong/grpcbin` + arch-keyed `grpcurl` in grpc-loadbalance).

3. Numbers are one representative cold run (fresh `destroy` first, so image
   layers are re-pulled into the new node containers). A warm re-run is
   faster; a roomier host is much faster — see the main README "Minimum host
   resources" for the validated 10-core reference shape.
