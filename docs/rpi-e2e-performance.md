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

- **Build:** ocibnkctl `5f6827d` · BNK 2.3.0 · CNE manifest 2.3.0-3.2598.3-0.0.170
- **Wall clock:** **20m59s** (from empty host to deployed + 12 scenarios)
- **Result:** deploy **6/6 ok** · scenarios **11/12 ok** (the one miss is a
  known tight-host timing flake — see notes)

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

### Scenarios — 12m8s

| Scenario | Status | Time |
|---|---|---|
| bgp-peer-frr | ok | 3m53s |
| ai-semantic-cache | ok | 31s |
| ai-token-counting | ok | 21s |
| cluster-wide-watch | ok | 6s |
| core-file-collection | ok | 3m2s |
| cwc-admin-access | flaked¹ | 10s |
| external-resource-pool | ok | 45s |
| grpc-loadbalance | ok | 33s |
| http-routing-e2e | ok | 47s |
| proxy-protocol-l4 | ok | 22s |
| tcp-l4-loadbalance | ok | 34s |
| udp-l4-loadbalance | ok | 1m4s |

`bgp-peer-frr` and `core-file-collection` are the long poles — both restart
the TMM pod (Multus NAD attach / coreCollection toggle) and wait for the
rollout, which is slow on a 4-core node.

## Notes

1. **cwc-admin-access** failed once here (10s) on `Authenticated GET /status
   returns HTTP 200 with license JSON`, then **passed on an idle retry**. It
   ran immediately after `core-file-collection` restarted TMM, so the CWC
   admin API momentarily served a not-yet-`Active` license under concurrent
   4-core load. It is timing-sensitive on a tight host, not a functional gap —
   all three of its assertions pass when the cluster is settled.

2. This shape exercises the Pi-specific fixes end-to-end: the node-container
   **DNS pin** (real upstream resolvers via `--dns`, so image pulls and
   CoreDNS don't depend on the flaky embedded-DNS-to-loopback proxy), the
   **auto `deploy-shrink`** phase, and arm64 image selection across all
   scenarios (e.g. `kong/grpcbin` + arch-keyed `grpcurl` in grpc-loadbalance).

3. Numbers are one representative cold run (fresh `destroy` first, so image
   layers are re-pulled into the new node containers). A warm re-run is
   faster; a roomier host is much faster — see the main README "Minimum host
   resources" for the validated 10-core reference shape.
