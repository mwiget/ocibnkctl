# BNK 2.4.0 public-doc errata (collected 2026-09-13 while upgrading ocibnkctl)

Base: https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/ ("BNK 2.4 (latest)")

1. **Stale page titles.** Many 2.4 pages still carry the `— BIG-IP Next for Kubernetes 2.3`
   HTML title (download-the-release-manifest…, system-requirements, prepare-kubernetes,
   install-and-configure-open-source-cert-manager). Confusing when the sidebar says 2.4.
2. **Release-manifest table lost the 2.3.x rows.** `download-the-release-manifest-and-create-its-companion-script.html`
   on `latest` lists only `2.4.0 → 2.4.0`. The 2.3 branch of the same page lists
   2.3.3 → 2.3.3-3.2598.3-0.0.509, 2.3.2 → …0.0.392, 2.3.1 → …0.0.304, 2.3.0 → …0.0.170.
   The 2.4.0 release notes say they describe "differences between … 2.3.3 and 2.4.0",
   yet 2.3.3 appears nowhere in the 2.4 docs.
3. **Release-manifest example is stale.** The "shortened example" on that page shows
   `charts/cwc 0.92.22-0.0.14`; the real 2.4.0 BOM pulled from repo.f5.com pins
   `charts/cwc 0.92.22-0.0.15` (verified with `ocibnkctl manifest probe 2.4.0`).
4. **Upgrade doc (bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html) has an author note
   inside a shell command:** `tar/f5-lifecycle-operator-v2.30.0-0.5.2.tgz \ # Need to change FLO to the correct version`.
   A `#` comment after a trailing `\` breaks line continuation — copy/paste fails.
5. Same upgrade doc: CNEInstance example file is named `cnegatewayclass-cr-2.4.0.yaml`
   and labelled `app.kubernetes.io/managed-by: kustomize`; uses namespaces `f5-utils` /
   `f5-alpha` while the fresh-install docs use `f5-cne-core` / `f5-bnk-instance`.
   Its `storageClassName: nfs` sits under `spec.dpu:` whereas the install doc puts
   `storageClassName` at `spec.` level.
6. **Release notes "Enhancements"** links the Debug API as a raw source path:
   "see /troubleshooting/debug-apis/spk-debug-apis.md".
7. **Release notes schema section** says "Removed the legacy BNKGateway CRD" while
   the breaking-changes section (and all prior docs) call it `F5BnkGateway`.
8. **Known issue 2455985-1** has a typo: `scrapetemplates.metrics.f5.net → crapetemplates.metrics.f5.com`.
9. **Install a CNE instance page** contains a stray `? -->` (leftover HTML comment
   terminator rendered as text) after the NAD `kubectl apply` step.
10. **System requirements** says Calico `v3.27.0` and cert-manager "Latest" while
    the cert-manager page still cites OpenShift 4.18 / cert-manager v1.19.6 as the
    example — no 1.35 / vanilla-k8s guidance.
11. **CRD index still documents `BNKGateway`** (custom-resource-definitions/bnk-bnkgateway.html)
    as a live, "optional" resource with `F5BnkGateway` samples and no deprecation banner,
    while the 2.4.0 release notes state "F5BnkGateway will no longer be valid".
    Same for F5SPKVlan / F5SPKEgress / F5SPKStaticRoute pages and the
    "Configure the Network" install page, which still instructs applying F5SPKVlan.
12. **NetPolicy CRD page**: under "NetPolicy with F5BigCneIrule attached to Gateway →
    Apply F5BigCneIrule CR" the YAML shown is an `F5BigPersistenceProfile`, not an iRule.
    The persistence example's NetPolicy still references `F5BigCneIrule irule-one`, and
    its `kubectl create` output says `persistence-netpolicy created` although
    `metadata.name` is `my-test-netpolicy`.
13. **Configure global BGP routing** how-to: BGP neighbor Secret uses
    `apiVersion: k8s.f5net.com/v1alpha1` / `kind: Secret` — must be core `v1`.
14. **Network configuration and application traffic management** page:
    - L4Route example uses `apiVersion: k8s.f5.com/v1` with `host/port/pool.service`
      fields; the real CRD is `gateway.k8s.f5.com/v1` with `parentRefs`/`rules`.
    - NetPolicy and SecPolicy examples use `apiVersion: k8s.f5.com/v1` and a singular
      `targetRef` + `irules:`; the CRD reference says `gateway.k8s.f5.com/v1alpha1`
      with `targetRefs[]` + `extensionRefs[]`.
    - F5BigCneIrule example uses `spec.irule`; the CRD field is `spec.iRule`.
    - Gateway API version stated as v1.5.0 here, "v1.4.1 experimental or later" in
      System requirements, "features from v1.4.0" on the Gateway API page, and
      "v1.2.0" in the SPK→BNK migration appendix.
    - Heading "iRules integration (NetPolicy)" appears twice; the first one contains
      the (wrong) L4Route example. "Trust the well-known CA set" is an empty code block.
    - BackendTLSPolicy shown as both `gateway.networking.k8s.io/v1alpha2` and `/v1`.
    - Migration appendix claims SPK networking CRs (F5SPKVlan, F5SPKStaticRoute)
      "do not need format changes" — contradicts the 2.4 breaking-changes list.
15. **GatewaySettings how-to** "Complete example" YAML has broken indentation
    (`name:`/`namespace:` at column 0 under `metadata:`) — not valid YAML as shown.
16. **Infra how-to**: only one step is numbered ("Step 4: Define networks").
17. **System requirements** says Helm "v3.x"; release notes recommend Helm 4.1 / 3.18.
18. **Proxy Protocol how-to**: L4Route shown with `apiVersion: gateway.networking.k8s.io/v1`
    (L4Route is an F5 CRD: `gateway.k8s.f5.com/v1`). The sample iRule's `encode` proc
    references `$ipv6_compressed` inside the `version eq 4` branch without ever
    defining it — the iRule as printed cannot run.
19. **External-resource LB how-to** links twice to an internal staging host
    (`clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/...`).
    Pool example is named `infra-backend-pool` but the HTTPRoute references `http-pool`.
20. **Gateway CRD page**: `infrastructure.parametersRef` field table still says
    "CNE controller support F5BnkGateway resource here" / kind example `F5BnkGateway`,
    while every example on the same page uses `GatewaySettings`.
21. **CNEInstance CRD page** does not document the `USE_GATEWAY_SETTINGS` cneController
    env var that the 2.4 install example relies on.
22. **HTTPRoute how-to** states Gateway API "version 1.4" while the traffic page says v1.5.0.
23. **Release notes vs shipped CRD bundle (observed on a live 2.4.0 install)**: the
    breaking-changes section says "F5BnkGateway will no longer be valid", yet the
    2.4.0 crd-installer still creates `f5-bnkgateways.k8s.f5net.com` (and
    `f5-spk-vlans` / `f5-spk-egresses` / `f5-spk-egresssips` / `f5-spk-staticroutes`,
    all `k8s.f5net.com`). Conversely the old `l4routes.gateway.k8s.f5net.com` and
    `bnknetpolicies.gateway.k8s.f5net.com` CRDs are gone outright, so a 2.3 manifest
    fails with `no matches for kind "L4Route" in version "gateway.k8s.f5net.com/v1"`.
    The notes only say the group "changed", not that the old group is removed.
    (Re-checked on a live 2.4.0 cluster, 2026-09-13.)
24. **Dead link on the 2.4 site**: `use-cases/bnk-ficforgatewayapi.html` ("F5 IPAM
    Controller for Gateway API", referenced by the ocibnkctl `fic-dynamic-ip` scenario)
    now serves the generic F5 Cloud Docs index; the 2.4 `use-cases/index.html` links
    only to the Vxlan CRD page. The Gateway API page still says "See F5 IPAM Controller
    for Gateway API" with no working target.
25. **Install docs vs shipped controller**: the 2.4 "Install a CNE instance" example
    sets `advanced.cneController.env USE_GATEWAY_SETTINGS=true`, but a fresh 2.4.0
    install via FLO runs f5-cne-controller with only `GATEWAY_API_VERSION=v1.4.1`; the
    variable is undocumented in the CNEInstance CRD reference, so whether it is
    required for GatewaySettings/Infra reconciliation is unknowable from the docs.
