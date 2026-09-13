# ocibnkctl → BNK 2.4.0 upgrade plan (drafted 2026-09-13)

## Facts established (no cluster touched)
- Latest public release: **2.4.0** (only 2.4.x; 2.3.3 exists on the 2.3 line: 2.3.3-3.2598.3-0.0.509).
- Release-manifest tag is plain **`2.4.0`** — verified with `manifest probe 2.4.0`
  (BOM releases[0].version == 2.4.0; 25 charts / 61 images; FLO v2.30.0-0.5.2,
  cert-gen 0.9.3, cwc 0.92.22-0.0.15, f5-cert-manager 0.27.9-0.0.2).
- New in BOM: f5-cert-manager-extension, f5-dpf, f5-epp, f5-access-*, f5-ipsec,
  f5-zonerunner, f5-toda-assistant, f5-gslb-debug-sidecar. Gone: f5-license-proxy,
  flp-setup, vault/postgresql images. OcNOS 0.23.0 → 0.44.0; tmm-img v10.159 → v10.204;
  f5dr-img v3.28 → v3.33; f5ing-tmm-pod-manager v1.6.1 → v1.10.11.
- Supported k8s (BNK/DPU): 1.35 and 1.30 → k3s v1.30.14 stays valid; 1.35 optional later.
- Breaking (release notes): gateway.k8s.f5net.com → gateway.k8s.f5.com; F5BnkGateway →
  GatewaySettings (+ Infra); BNKNetPolicy → NetPolicy; BNKSecPolicy → SecPolicy;
  F5SPKVlan/F5SPKStaticRoute/VRF/VxLAN → Infra; F5SPKEgress → EgressGateway.
  Unchanged group k8s.f5net.com/v1: License, Pool, F5BigCneIrule, F5Big*.
- 2.4 CNEInstance adds cneController env `USE_GATEWAY_SETTINGS=true`; new
  `spec.placement.{dataPlane,controlPlane,observability,sessionState}.nodeSelector`.
- Behaviour change: VIP stays advertised + HTTP 500 when no active pool members
  (was: VS removed + VIP withdrawn).
- Host: 28 cores / 188 GB — no shrink needed. Two live clusters (bnk232 = 2.3.2
  baseline at ~/git/bnk232, edge_octet 98; scope). Edge octets in use: 98,100,101,102.

## Phase 0 — freeze (done locally)
- `release/2.3.2` branch created at 5436b3d (== tag v2.3.2). NOT pushed.
- Feature branch for the work: `feat/bnk-2.4.0`.

## Phase 1 — pin bump, cluster-free
1. version.go: CNEManifestVersion=2.4.0; BNKVersion 2.4.0 (Makefile BNK, .goreleaser.yaml x2).
2. Docs/strings: README badge/table, AGENTS.md, CLAUDE.md, manifest.go example, schema.go comments.
3. Unit tests referencing 2.3.2 tag; `make smoke`.
4. Commit docs/bnk-2.4.0-doc-errata.md + this plan.
Exit: `make smoke` green, `ocibnkctl version` shows 2.4.0 / manifest 2.4.0.

## Phase 2 — first 2.4.0 deploy, minimal-change (empirical gate)
New PoC `~/git/bnk240` (tmm_nodes 2, edge_octet 99, registry cache on), keep every
2.3 CR shape (legacy F5BnkGateway, OcNOS ConfigMap, no USE_GATEWAY_SETTINGS).
`e2e --yolo` and watch for:
- FLO v2.30 chart values drift (sharedComponentNamespace, f5-ipam-operator namespace, new required values).
- crd-installer set: is f5-spk-crds-deprecated still installing F5BnkGateway? (release notes: "no longer valid")
- License CRD group (still licenses.k8s.f5net.com per 2.4 doc) and CWC cert-gen flow.
- TMM demoMode still honoured; TMM_MAPRES_* / TMM_IGNORE_MEM_LIMIT / hugepage caps on tmm v10.204.
- OcNOS 0.44 + f5-tmm-dynamic-routing-template ConfigMap + imish passwd.conf + redistribute kernel.
- pod-manager v1.10 resource enforcement (shrink/small-host assumptions).
Exit: 6/6 phases OK; bgp-anycast + bgp-peer-frr green. Record every deviation in the errata/journal.

## Phase 3 — CRD migration in the tool + scenarios
A. deploy cne: apply an `Infra` CR (singleton, BNK ns `default`) with ipams=external VIP
   pool (203.0.113.100-200) and, if the CRD really requires networks/networkAttachments,
   a tag-0 vlan over the bnk-bgp NAD — THEN test whether that steals net1 from OcNOS
   (mapres). Decide: (a) Infra+GatewaySettings with USE_GATEWAY_SETTINGS=true, or
   (b) static-address Gateways with no parametersRef (documented as valid in 2.4) and no
   F5BnkGateway at all. Prefer (b) for the default path if it works — smallest surface.
B. Scenario manifests (mechanical): gateway.k8s.f5net.com → gateway.k8s.f5.com
   (L4Route, Gateway allowedRoutes.kinds.group); BNKNetPolicy → NetPolicy
   (gateway.k8s.f5.com/v1alpha1) in proxy-protocol-l4 + ai-token-count-dssm;
   02-bnkgateway.yaml in 11 scenarios → GatewaySettings (or drop, per A).
C. fic-dynamic-ip: rewrite around GatewaySettings.ingressConfig.defaultListenerNetwork.ipamRefs
   → Infra ipams (this may finally turn it green — 2.3 had no F5BnkGateway→IPAM bridge).
D. selfip-dag dataplane mode (F5SPKVlan in activeactive.go): port to Infra vlan network
   or mark unsupported on 2.4 (poc validate error). Decision for the user.
E. Verify code: any assertion on VIP withdrawal / VS removal must follow the new
   "VIP stays, 500" behaviour.
Exit: `scenario run --all` green set ≥ the 14 that were green on 2.3.2.

## Phase 4 — hardening + release
- Re-run e2e from scratch (fresh PoC) + all scenarios; update ratings/Descriptions.
- Update docs/dataplane-modes.md, README scenario table, network topology if Infra changed it.
- Optional: k3s v1.35 node image trial (rancher/k3s:v1.35.x-k3s1) — separate PR.
- Optional: `deploy upgrade` path 2.3.x→2.4.0 on the live bnk232 cluster (FLO helm upgrade +
  CNEInstance manifestVersion bump; needs KI 2455985-1 metrics CRD rename workaround).
- Tag v2.4.0 (first cut), sibling tools (dpubnkctl/tmmlitectl) later.
