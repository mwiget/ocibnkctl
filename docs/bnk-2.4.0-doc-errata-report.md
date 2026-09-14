# BNK 2.4.0 public-doc errata: report for the F5 docs team

**Title:** BNK 2.4 public docs: release notes omit USE_GATEWAY_SETTINGS and CRD removals, plus 25 errata (broken examples, wrong apiVersions, internal staging links, stale 2.3 content)

**Docs:** https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/ (BNK 2.4)
**Full list with details:** https://github.com/mwiget/ocibnkctl/blob/main/docs/bnk-2.4.0-doc-errata.md (item numbers below refer to it)
**Collected:** 2026-09-13, while moving ocibnkctl to BNK 2.4.0 and validating the Gateway-API how-tos on a live 2.4.0 cluster. Items #19 and #24 re-checked on the live site the same day, and the CRD presence and removal in the first section re-checked on a live 2.4.0 cluster.

## Release notes gaps that block a working 2.4.0 deploy (highest priority)

**1. `USE_GATEWAY_SETTINGS` isn't in the release notes (#21, #25)**
- The 2.4.0 release notes don't mention `USE_GATEWAY_SETTINGS` anywhere. They introduce Infra and GatewaySettings as the replacements for F5BnkGateway and the F5SPK* network CRs, but never say the CNE controller has to be told to use them.
- The CNEInstance CRD reference doesn't document the variable either. It only appears in the "Install a CNE instance" example, as `advanced.cneController.env USE_GATEWAY_SETTINGS=true`.
- A fresh 2.4.0 install through FLO doesn't set it: without an explicit CNEInstance override, f5-cne-controller runs with only `GATEWAY_API_VERSION=v1.4.1`.
- Observed on a live 2.4.0 cluster: without `USE_GATEWAY_SETTINGS=true`, the `Infra` and `GatewaySettings` resources stay at "Waiting for controller" and never reconcile. Once ocibnkctl set it through `CNEInstance.spec.advanced.cneController.env`, they reached Programmed.
- Ask: document the variable in the release notes' breaking changes, the upgrade guides and the CNEInstance CRD reference. Say whether it's required for Infra/GatewaySettings and why FLO doesn't set it by default.

**2. The release notes don't say which old CRDs are removed (#23, #7)**
- The notes say both "F5BnkGateway will no longer be valid" and "Removed the legacy BNKGateway CRD". That's two names for one CRD, and "no longer valid" doesn't say whether the resource still exists.
- What 2.4.0 actually ships (live cluster, fresh install through FLO):
  - **Still installed** by the 2.4.0 crd-installer: `f5-bnkgateways.k8s.f5net.com`, `f5-spk-vlans.k8s.f5net.com`, `f5-spk-egresses.k8s.f5net.com`, `f5-spk-egresssips.k8s.f5net.com`, `f5-spk-staticroutes.k8s.f5net.com`. The controller no longer uses an F5BnkGateway: a Gateway without `infrastructure.parametersRef` is programmed from `spec.addresses` ("FIC deployed but no infrastructureRef, using Spec.Addresses").
  - **Removed outright**: `l4routes.gateway.k8s.f5net.com` and `bnknetpolicies.gateway.k8s.f5net.com` (replaced by `l4routes.gateway.k8s.f5.com` and `netpolicies.gateway.k8s.f5.com`). The notes only say the API group "changed" and the CRD was "renamed". A 2.3-style manifest now fails outright instead of warning that it's deprecated: `no matches for kind "L4Route" in version "gateway.k8s.f5net.com/v1" — ensure CRDs are installed first`.
- Upgrade impact: an operator can't tell from the notes which 2.3 resources will fail to apply after the upgrade (L4Route, BNKNetPolicy) and which will still apply but do nothing (F5BnkGateway, F5SPKVlan). There's no before/after YAML for any of the renames.
- Ask: add a table to the release notes and the 2.3.x → 2.4.0 upgrade guides listing each 2.3 CRD as removed, still installed but ignored, or renamed (old → new group/kind), with a minimal before/after manifest per rename. Use one name for the removed CRD.

## Examples that fail as written
- #4 Upgrade 2.3.x→2.4.0 (DPU, FLO): an author note (`# Need to change FLO to the correct version`) sits after a trailing `\`, which breaks the command's line continuation.
- #13 Configure global BGP routing: the BGP neighbor Secret uses `apiVersion: k8s.f5net.com/v1alpha1`; it must be core `v1`.
- #14 Network configuration and application traffic management: the L4Route, NetPolicy, SecPolicy and F5BigCneIrule examples use the wrong API group or version and the wrong field names (`k8s.f5.com/v1`, singular `targetRef`, `spec.irule`). The CRDs use `gateway.k8s.f5.com`, `targetRefs[]`/`extensionRefs[]` and `spec.iRule`. The "Trust the well-known CA set" code block is empty.
- #15 GatewaySettings how-to: the "Complete example" YAML isn't valid (bad `metadata:` indentation).
- #18 Proxy Protocol how-to: L4Route is shown under `gateway.networking.k8s.io/v1`, but it's an F5 CRD (`gateway.k8s.f5.com/v1`). The sample iRule uses `$ipv6_compressed` without defining it, so it can't run.
- #12 NetPolicy CRD page: the "Apply F5BigCneIrule CR" step shows an `F5BigPersistenceProfile`, and the example's names don't match its output.

## Internal or dead links
- #19 Configure external-resource load balancing: two links point to the internal staging host `clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/...`. The pool is named `infra-backend-pool`, but the HTTPRoute references `http-pool`.
- #24 `use-cases/bnk-ficforgatewayapi.html` ("F5 IPAM Controller for Gateway API") now serves the generic F5 Cloud Docs page, yet the Gateway API page still links to it.
- #6 The release notes link the Debug API as a raw source path (`/troubleshooting/debug-apis/spk-debug-apis.md`).

## 2.4 docs vs. the 2.4 model and the shipped bits
- #11 and #20: the BNKGateway, F5SPKVlan, F5SPKEgress and F5SPKStaticRoute CRD pages, the "Configure the Network" install page and the Gateway `parametersRef` table still present the removed resources as current, with no deprecation banner.
- #5 The upgrade doc's CNEInstance example uses different file naming, labels and namespaces from the install docs, and puts `storageClassName` in a different place.
- #23, #21, #25 and #7 are covered in detail in the first section above.

## Stale or inconsistent versions and metadata
- #1 Many 2.4 pages still have "BIG-IP Next for Kubernetes 2.3" in the HTML title.
- #2 The release-manifest table on `latest` dropped the 2.3.x rows, and 2.3.3 (the baseline the 2.4.0 release notes compare against) appears nowhere in the 2.4 docs.
- #3 The release-manifest example shows `charts/cwc 0.92.22-0.0.14`, but the real 2.4.0 BOM pins `0.92.22-0.0.15`.
- #10 and #17: System requirements lists Calico v3.27.0, cert-manager "Latest" and Helm "v3.x", while the cert-manager page cites OpenShift 4.18 / cert-manager v1.19.6 and the release notes recommend Helm 4.1 or 3.18.
- #14 and #22: the Gateway API version is given as v1.5.0, v1.4.1, v1.4.0, "1.4" and v1.2.0 on different pages. BackendTLSPolicy appears as both `v1alpha2` and `v1`. The SPK→BNK migration appendix says SPK networking CRs need no changes, contradicting the breaking-changes list.

## Typos and rendering
- #8 Known issue 2455985-1 says "crapetemplates" instead of "scrapetemplates".
- #9 The "Install a CNE instance" page shows a stray `? -->` after the NAD `kubectl apply` step.
- #16 The Infra how-to numbers only one step ("Step 4").
- #14 The heading "iRules integration (NetPolicy)" appears twice.
