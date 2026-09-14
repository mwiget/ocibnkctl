# BNK 2.4.0 public-doc errata: report for the F5 docs team

**Title:** BNK 2.4 public docs: release notes omit USE_GATEWAY_SETTINGS and CRD removals, plus 24 errata (broken examples, wrong apiVersions, internal staging URLs, stale 2.3 content)

**Docs:** https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/ (BNK 2.4), compared against https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/
**Full list with quotes, anchors and 2.3 comparison:** https://github.com/mwiget/ocibnkctl/blob/main/docs/bnk-2.4.0-doc-errata.md (item numbers below refer to it)
**Collected:** 2026-09-13, while moving ocibnkctl to BNK 2.4.0 and validating the Gateway-API how-tos on a live 2.4.0 cluster.
**Re-verified:** 2026-09-14, item by item against the raw page HTML, the 2.3 docs and a freshly installed 2.4.0 cluster. One item (#9) was withdrawn and several were reworded.

Tags: **[new]** = introduced in the 2.4 docs; **[2.3]** = carried over unchanged from the 2.3 docs.

## Release notes gaps that block a working 2.4.0 deploy (highest priority)

**1. `USE_GATEWAY_SETTINGS` isn't in the release notes (#21, #25) [new]**

- **The docs barely mention it.**
  - The variable appears nowhere in the [2.4.0 release notes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#breaking-changes), which introduce Infra, GatewaySettings and EgressGateway as the replacements for F5BnkGateway and the F5SPK* CRs.
  - It's absent from the [CNEInstance CRD reference](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/cneinstance-crd.html) and both 2.3.x→2.4.0 upgrade guides.
  - Across the whole 2.4 site it appears in exactly one place: the example in the DPU [Install a CNE instance using FLO](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads) (`advanced.cneController.env USE_GATEWAY_SETTINGS=true`, the only content change from the 2.3 version of that page).
  - The [Host FLO](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-self-managed-flo/install/install-a-cne-instance-using-flo.html) and [Host Helm](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-cnfs-self-managed-helm/install/install-a-cne-instance-using-helm.html) install pages don't set it.
- **FLO doesn't set it by default.** With no override in the CNEInstance, the `CNEController` CR carries no such variable, and f5-cne-controller `v14.91.12-0.4.7` runs with `GATEWAY_API_VERSION=v1.4.1` as its only Gateway-related variable.
- **Without it, Infra and GatewaySettings are ignored.** Verified on a live 2.4.0 cluster on 2026-09-14 by removing the variable, then restoring it:
  - A new GatewaySettings and a new Infra stayed `Accepted=Unknown` "Pending: Waiting for controller" for 6 and 4 minutes. Their `lastTransitionTime` stayed at the CRD default 1970-01-01, meaning the controller never wrote status.
  - A Gateway referencing the GatewaySettings through `infrastructure.parametersRef` stayed `Programmed=False` "Referenced GatewaySettings … not found", with no address.
  - Deleting an existing Infra hung on its `handletmmconfig_inconsistency` finalizer.
  - A Gateway with static `spec.addresses` was programmed normally.
  - Within about 25 seconds of restoring `USE_GATEWAY_SETTINGS=true`, Infra and GatewaySettings reached `Accepted`/`ResolvedRefs`/`Programmed=True`, and the Gateway got 203.0.113.110 from the Infra IPAM pool.
  - Full timeline in #25.
- Impact: an operator who follows the Host install pages or the DPU upgrade guide gets a controller on which the new 2.4 network model silently does nothing.
- Ask: document the variable in the release notes' breaking changes, both upgrade guides, every CNE-instance install page and the CNEInstance CRD reference. Say that it's required for Infra/GatewaySettings, and either make it the FLO default or explain why it isn't.

**2. The release notes don't say which old CRDs are removed (#23, #7) [new]**
- The notes use two names for one CRD:
  - [schema changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#helm-chart-and-custom-resource-schema-changes): "Removed the legacy BNKGateway CRD"
  - [breaking changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#breaking-changes): "F5BnkGateway will no longer be valid"
- For L4Route and the policies they only say "The API group gateway.k8s.f5net.com has changed to gateway.k8s.f5.com" and "BNKSecPolicy and BNKNetPolicy have been renamed".
- What a fresh 2.4.0 install through FLO actually ships (live cluster, crd-installer `v14.91.12-0.4.7`):
  - **Still installed, served, and not marked deprecated:** `f5-bnkgateways.k8s.f5net.com`, `f5-spk-vlans`, `f5-spk-egresses`, `f5-spk-egresssips`, `f5-spk-staticroutes` and `f5-spk-snatpools` (all `k8s.f5net.com`). A Gateway without `infrastructure.parametersRef` is programmed from `spec.addresses` ("FIC deployed but no infrastructureRef, using Spec.Addresses"), so no F5BnkGateway is needed.
  - **Removed outright:** every CRD in `gateway.k8s.f5net.com`. A 2.3-style manifest fails even a server-side dry-run: `no matches for kind "L4Route" in version "gateway.k8s.f5net.com/v1" — ensure CRDs are installed first`.
- The notes say F5SPKVlan and F5SPKStaticRoute are consolidated into **Infra**, F5SPKEgress is replaced by **EgressGateway**, and F5BnkGateway by **GatewaySettings**. They don't say those old CRDs remain installed.
- Upgrade impact: after an upgrade, some 2.3 resources fail to apply (L4Route, BNKNetPolicy, BNKSecPolicy). Others still apply but are superseded (F5BnkGateway, F5SPKVlan, F5SPKStaticRoute, F5SPKEgress). An operator can't tell from the notes which is which.
- Neither the [DPU](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html) nor the [Host](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-upgrade-2.3.x-to-2.4.0-using-helm.html) upgrade guide mentions Infra, GatewaySettings, EgressGateway, NetPolicy or the API-group change. The only rename table covers the Observer CRDs.
- Ask: add a table to the release notes and both upgrade guides listing each 2.3 CRD as removed, still installed but superseded, or renamed (old → new group/kind), with a minimal before/after manifest per rename. Use one name (the kind, `F5BnkGateway`).

## Examples that fail as written
- **#4 [new]** [DPU upgrade guide, Upgrade the FLO](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html#upgrade-the-f5-lifecycle-operator-flo)
  - `tar/f5-lifecycle-operator-v2.30.0-0.5.2.tgz \ # Need to change FLO to the correct version` has a space after the `\`, so the command ends there and `-f …flo-values_2.4.0.yaml` runs as a separate command.
  - The note also flags the chart version as unconfirmed.
- **#13 [new]** [Configure global BGP routing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/configure-global-BGP-routing.html#procedure) and twice on [BGP sample files](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/sample-files-by-use-case.html): the BGP neighbor Secret uses `apiVersion: k8s.f5net.com/v1alpha1`; it must be core `v1`.
- **#14 [new]** [Network configuration and application traffic management](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html)
  - **L4Route:** shown as `k8s.f5.com/v1` with `host`/`port`/`pool`. The real CRD is `gateway.k8s.f5.com/v1` with `parentRefs`/`rules`.
  - **NetPolicy and SecPolicy:** shown as `k8s.f5.com/v1` with a singular `targetRef` plus `irules:` or `firewallPolicies:`. The real CRDs are `gateway.k8s.f5.com/v1alpha1` with `targetRefs[]`/`extensionRefs[]`.
  - **F5BigCneIrule:** shown with `spec.irule`. The real field is `spec.iRule`.
  - **BackendTLSPolicy:** shown as `v1alpha2`, which 2.4.0 doesn't serve (`v1`, `v1alpha3` only).
  - **"Trust the well-known CA set":** the code block contains only a leaked "```yaml" fence.
- **#15 [new]** [GatewaySettings how-to, Complete example](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-tenant-traffic-settings-with-gatewaysettings.html#complete-example): the keys under `metadata:` and `spec:` are all at column 0, so both parse as null and the object is rejected.
- **#18** [Proxy Protocol how-to](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html)
  - **[2.3]** The L4Route is shown under `gateway.networking.k8s.io/v1`, but it's an F5 CRD (`gateway.k8s.f5.com/v1`).
  - **[2.3]** The sample iRule's `encode` proc uses `$ipv6_compressed` without defining it, and is truncated (no encoding, no `return`), so it can't work.
  - **[new]** The Step 4 NetPolicy omits the `group`/`kind` its CRD requires.
- **#12 [new]** [NetPolicy CRD page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-NetPolicy.html#netpolicy-with-f5bigcneirule-attached-to-gateway)
  - The "Apply F5BigCneIrule CR" step shows an `F5BigPersistenceProfile`.
  - The persistence example's NetPolicy references the iRule, not the profile.
  - The example names don't match their `kubectl` output.
  - The 2.3 page was correct.

## Internal or dead links
- **#19 [2.3]** [Configure external-resource load balancing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html#before-you-begin)
  - Two bare-text URLs point to the internal staging host `clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/...`.
  - The Pool is named `infra-backend-pool`, but both HTTPRoutes reference `http-pool`.
- **#24** FIC for Gateway API page
  - **[new]** `use-cases/bnk-ficforgatewayapi.html` now 301-redirects to the Cloud Docs home page. The content moved to [components/bnk-ficforgatewayapi.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/components/bnk-ficforgatewayapi.html) with no redirect, and the [Use cases section](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/use-cases/index.html) is now empty.
  - **[2.3]** The [Gateway API page's](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-gateway-api.html#using-f5-ipam-controller) "See F5 IPAM Controller for Gateway API" is an unresolved cross-reference (`#/how-tos/bnk-ficforgatewayapi.md`).
- **#6 [new]** The [release notes Enhancements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#enhancements) link the Debug API through an unresolved cross-reference rendered as `/troubleshooting/debug-apis/spk-debug-apis.md`. No such page exists; the target is probably [Debug APIs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/troubleshooting/debug-apis/index.html).

## 2.4 docs vs. the 2.4 model and the shipped bits
- **#11 [2.3]** These pages are unchanged from 2.3, with no deprecation note or pointer to Infra/EgressGateway/GatewaySettings, and remain in the navigation:
  - the [BNKGateway](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-bnkgateway.html) ("optional"), [F5SPKVlan](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-vlan-crd.html), [F5SPKEgress](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-egress-crd.html) and [F5SPKStaticRoute](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-static-route-crd.html) CRD pages
  - [Configure the Network](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-configure-network.html#apply-internal-and-external-f5spkvlan-cr), which still says to apply F5SPKVlan
- **#20 [new]** The [Gateway CRD parameter table](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-gateway.html#cr-parameters) still says `parametersRef` takes an F5BnkGateway, while every example on the page uses GatewaySettings.
- **#5 [2.3]** The [DPU upgrade guide's CNEInstance](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html#apply-the-cne-instance) is the 2.3.x example with a version bump.
  - It has `cnegatewayclass-cr-2.4.0.yaml`, the kustomize label, `f5-alpha`/`f5-utils`, `arm-ca-cluster-issuer` and `far-secret`.
  - It doesn't match the [install doc](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads), and it omits `USE_GATEWAY_SETTINGS=true`.
- #23, #21, #25 and #7 are covered in the first section above.

## Stale or inconsistent versions and metadata
- **#1 [new]** Every 2.4 page sampled (159/159) carries `<meta name="version" content="2.3">` and a Sphinx title ending "BIG-IP Next for Kubernetes 2.3", e.g. [System requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html). Each page also contains two `<html>/<head>` blocks.
- **#2 [new]** The [release-manifest table](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#set-the-release-manifest-version) on `latest` dropped the 2.3.x rows. 2.3.3, the baseline the [release notes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#introduction) compare against, is mentioned only in that one sentence on the whole 2.4 site.
- **#3** The [example release manifest](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#example-release-manifest) shows `charts/cwc 0.92.22-0.0.14`, but the real 2.4.0 BOM pins `0.92.22-0.0.15`. The Host upgrade guide also uses `0.0.14`. The 2.3 page showed a 2.2.0 manifest, so this is a recurring pattern.
- **#10 [2.3]** DPU [system requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#software-requirements) list Calico v3.27.0 and cert-manager "Latest".
  - The [cert-manager page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/install-and-configure-open-source-cert-manager.html#install-open-source-cert-manager) gives only an OpenShift 4.18 → v1.19.6 example, even on the vanilla-k8s-only DPU track.
  - No install page covers k8s 1.35, which the release notes list.
- **#17 [2.3]** Helm guidance contradicts itself:
  - [System requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#software-requirements) say "v3.x".
  - The [release notes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#supported-container-orchestration-platforms) recommend Helm 4.1 for k8s 1.35 and OpenShift 4.22.
  - [Prepare your computer](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-cnfs-self-managed-helm/prepare-your-computer-and-cluster/prepare-your-computer.html) says f5-cert-manager "isn't currently compatible with Helm 4.0 or later".
  - The [rollback guide](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-rollback-2.4.0-to-2.3.x-using-helm.html) says CWC rollback fails on Helm 4.
- **#14 / #22** Gateway API version:
  - 2.4.0 ships the v1.4.1 CRDs, which the [system requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#minimum-gateway-api-version) and the [HTTPRoute how-to](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/Configure-HTTP-traffic-steering-with-Gateway-API-HTTPRoute.html#overview) match.
  - **[new]** The [traffic page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#gateway-api-setup) says v1.5.0.
  - The SPK→BNK migration appendix says v1.2.0. Its [migration steps](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#migration-steps) also say exported CRs (including L4Route) reapply unchanged, which the API-group change contradicts.

## Typos and rendering
- **#8 [new]** [Known issue 2455985-1](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#known-issues) says "crapetemplates" instead of "scrapetemplates".
- **#16 [new]** On the [Infra how-to](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-network-infrastructure-with-infra-crd.html#create-the-infra-cr), all six steps render as "1."; only one carries a literal "Step 4:" prefix.
- **#14 [new]** The heading "iRules integration (NetPolicy)" appears twice on the traffic page.
