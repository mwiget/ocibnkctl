# BNK 2.4.0 public-doc errata

Collected 2026-09-13 while upgrading ocibnkctl to BNK 2.4.0. **Re-verified item by
item on 2026-09-14** against the live pages (raw HTML via `curl`, not rendered
summaries), the 2.3 public docs, the ocibnkctl implementation, and a live 2.4.0
cluster.

- **2.4 docs** ("BNK 2.4 (latest)"): <https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/>
- **2.3 docs** ("BNK 2.3" in the version selector): <https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/>
- Many install pages exist in three install-track copies with the same text
  (`bnk-dpu-self-managed-flo`, `bnk-host-self-managed-flo`,
  `bnk-host-cnfs-self-managed-helm`); links below point at the DPU copy unless
  a difference matters.

Each item carries:

- **Verdict**: *Confirmed*, *Corrected* (the core claim holds but the original
  wording was off; the text below is the corrected version), or *Withdrawn*.
- **Since**: *new in 2.4* or *carried over from 2.3*.

In the summary, **[new]** marks an issue introduced in the 2.4 docs and
**[2.3]** one carried over unchanged from the 2.3 docs.

## Summary for the F5 docs team

**Suggested issue title:** BNK 2.4 public docs: release notes omit USE_GATEWAY_SETTINGS and CRD removals, plus 24 errata (broken examples, wrong apiVersions, internal staging URLs, stale 2.3 content)

Item numbers (#N) refer to the detailed entries under [Details by item](#details-by-item), which carry the quotes, anchors and 2.3 comparison.

### Release notes gaps that block a working 2.4.0 deploy (highest priority)

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

### Examples that fail as written
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

### Internal or dead links
- **#19 [2.3]** [Configure external-resource load balancing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html#before-you-begin)
  - Two bare-text URLs point to the internal staging host `clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/...`.
  - The Pool is named `infra-backend-pool`, but both HTTPRoutes reference `http-pool`.
- **#24** FIC for Gateway API page
  - **[new]** `use-cases/bnk-ficforgatewayapi.html` now 301-redirects to the Cloud Docs home page. The content moved to [components/bnk-ficforgatewayapi.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/components/bnk-ficforgatewayapi.html) with no redirect, and the [Use cases section](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/use-cases/index.html) is now empty.
  - **[2.3]** The [Gateway API page's](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-gateway-api.html#using-f5-ipam-controller) "See F5 IPAM Controller for Gateway API" is an unresolved cross-reference (`#/how-tos/bnk-ficforgatewayapi.md`).
- **#6 [new]** The [release notes Enhancements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#enhancements) link the Debug API through an unresolved cross-reference rendered as `/troubleshooting/debug-apis/spk-debug-apis.md`. No such page exists; the target is probably [Debug APIs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/troubleshooting/debug-apis/index.html).

### 2.4 docs vs. the 2.4 model and the shipped bits
- **#11 [2.3]** These pages are unchanged from 2.3, with no deprecation note or pointer to Infra/EgressGateway/GatewaySettings, and remain in the navigation:
  - the [BNKGateway](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-bnkgateway.html) ("optional"), [F5SPKVlan](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-vlan-crd.html), [F5SPKEgress](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-egress-crd.html) and [F5SPKStaticRoute](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-static-route-crd.html) CRD pages
  - [Configure the Network](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-configure-network.html#apply-internal-and-external-f5spkvlan-cr), which still says to apply F5SPKVlan
- **#20 [new]** The [Gateway CRD parameter table](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-gateway.html#cr-parameters) still says `parametersRef` takes an F5BnkGateway, while every example on the page uses GatewaySettings.
- **#5 [2.3]** The [DPU upgrade guide's CNEInstance](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html#apply-the-cne-instance) is the 2.3.x example with a version bump.
  - It has `cnegatewayclass-cr-2.4.0.yaml`, the kustomize label, `f5-alpha`/`f5-utils`, `arm-ca-cluster-issuer` and `far-secret`.
  - It doesn't match the [install doc](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads), and it omits `USE_GATEWAY_SETTINGS=true`.
- #23, #21, #25 and #7 are covered in the first section above.

### Stale or inconsistent versions and metadata
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

### Typos and rendering
- **#8 [new]** [Known issue 2455985-1](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#known-issues) says "crapetemplates" instead of "scrapetemplates".
- **#16 [new]** On the [Infra how-to](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-network-infrastructure-with-infra-crd.html#create-the-infra-cr), all six steps render as "1."; only one carries a literal "Step 4:" prefix.
- **#14 [new]** The heading "iRules integration (NetPolicy)" appears twice on the traffic page.

---

## Details by item

### 1. Site-wide stale "2.3" version metadata

**Verdict:** Corrected (it's every page, not "many"; the visible tab title isn't affected) · **Since:** new in 2.4

Every 2.4 page sampled (159 of 159 across install/, how-tos/,
custom-resource-definitions/, releases/ and the other section indexes) still
carries `<meta name="version" content="2.3" />` and a Sphinx
`<title>… — BIG-IP Next for Kubernetes 2.3</title>`. No page says 2.4 except the
sidebar button "BNK 2.4 (latest)".

Each page is actually two HTML documents concatenated: an F5 wrapper `<head>`
with a plain title (e.g. `<title>System requirements</title>`), then the full
Sphinx document with its own `<head>`/`<title>`. Browsers use the first title
for the tab, so the tab probably doesn't show "2.3". Search indexers read the
version meta tag, so 2.4 content is filed under 2.3.

- Examples:
  [system-requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html),
  [download-the-release-manifest…](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html),
  [prepare-kubernetes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/prepare-kubernetes.html),
  [install-and-configure-open-source-cert-manager](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/install-and-configure-open-source-cert-manager.html),
  [landing page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/).
- 2.3 comparison: the [2.3 site](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/)
  correctly says 2.3 and the 2.2 site says 2.2, so the bump was missed only for 2.4.

### 2. Release-manifest table dropped the 2.3.x rows, and 2.3.3 is nearly absent from the 2.4 site

**Verdict:** Corrected (2.3.3 is mentioned exactly once) · **Since:** new in 2.4

- 2.4: [Set the release manifest version](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#set-the-release-manifest-version)
  lists one row: `2.4.0 | 2.4.0`.
- 2.3: the [same section on the 2.3 site](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#set-the-release-manifest-version)
  lists 2.3.3 → `2.3.3-3.2598.3-0.0.509`, 2.3.2 → `…0.0.392`, 2.3.1 → `…0.0.304`
  and 2.3.0 → `…0.0.170`.
- The [2.4.0 release notes introduction](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#introduction)
  says: "These release notes describe the differences between BIG-IP Next for
  Kubernetes (DPU and Host) 2.3.3 and 2.4.0."
- That sentence is the only mention of 2.3.3 on the 2.4 site. There are no 2.3.3
  release notes, manifest row or upgrade path there; the upgrade guides say
  "2.3.x". Readers must switch to the 2.3 site
  ([2.3.3 release notes](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/releases/release-notes-bnk-2.3.3.html)).
- Dropping older rows per version branch may be deliberate. The gap is that
  the baseline the 2.4.0 release notes compare against can't be found from the
  2.4 site.

### 3. Release-manifest example is one cwc build behind

**Verdict:** Confirmed · **Since:** carried over as a pattern (the 2.3 page showed a 2.2.0 manifest)

- 2.4: the [example release manifest](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#example-release-manifest)
  ("This is a shortened example of a release manifest") shows
  `charts/cwc` `version: 0.92.22-0.0.14`.
- The real 2.4.0 BOM pulled from repo.f5.com pins `charts/cwc 0.92.22-0.0.15`
  (`ocibnkctl manifest probe 2.4.0 --far keys/f5-far-auth-key.tgz`, re-run
  2026-09-14). The example's other charts (f5-cert-gen 0.9.3,
  f5-cert-manager 0.27.9-0.0.2) do match.
- The [Host 2.3.x→2.4.0 Helm upgrade guide](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-upgrade-2.3.x-to-2.4.0-using-helm.html)
  also uses `tar/cwc-0.92.22-0.0.14.tgz`.
- 2.3 comparison: the [2.3 page's example](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/install/bnk-dpu-self-managed-flo/prepare-to-install/download-the-release-manifest-and-create-its-companion-script.html#example-release-manifest)
  shows a 2.2.0 manifest (`2.2.0-3.2226.0-0.0.385`, `charts/cwc 0.49.7-0.0.16`).
  So the example is recurringly not regenerated at GA. Minor on its own, since
  it's labelled an example.

### 4. DPU upgrade guide: author note after a line-continuation backslash

**Verdict:** Confirmed · **Since:** new in 2.4

- [Upgrade the F5 Lifecycle Operator (FLO)](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html#upgrade-the-f5-lifecycle-operator-flo):
  ```
  $ helm upgrade f5-lifecycle-operator \
  tar/f5-lifecycle-operator-v2.30.0-0.5.2.tgz \ # Need to change FLO to the correct version
  -f arm/tmp/flo-values_2.4.0.yaml
  ```
- In the raw HTML the `\` is followed by a space and then the comment, so the
  backslash escapes the space, not the newline. The command ends on that line
  and `-f arm/tmp/flo-values_2.4.0.yaml` runs as a separate command.
- The note also means the chart version `v2.30.0-0.5.2` is flagged by the
  author as unconfirmed.
- 2.3 comparison: the 2.3 DPU upgrade guides (e.g.
  [2.3.2→2.3.3](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/releases/bnk-dpu-upgrade-2.3.2-to-2.3.3-using-flo.html))
  end the line with a clean `\`.
- The 2.4 Host Helm [upgrade](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-upgrade-2.3.x-to-2.4.0-using-helm.html)
  and [rollback](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-rollback-2.4.0-to-2.3.x-using-helm.html)
  guides don't have this problem.

### 5. DPU upgrade guide: CNEInstance example is a 2.3.x leftover that doesn't match the 2.4 install doc

**Verdict:** Corrected (the original `storageClassName` placement point was **wrong**; the rest holds) · **Since:** carried over from 2.3

Compared the upgrade guide's [Apply the CNE instance](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html#apply-the-cne-instance)
with [Install a CNE instance → Create a manifest for the CNE-instance workloads](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads):

| | Upgrade guide | Install doc |
|---|---|---|
| File name | `cnegatewayclass-cr-2.4.0.yaml` | `cne-instance-${CNE_INSTANCE_NAMESPACE}.yaml` |
| Label | `app.kubernetes.io/managed-by: kustomize` | none |
| Namespaces | `f5-alpha` (instance), `f5-utils` (pods checked) | `f5-bnk-instance`, `f5-cne-core` |
| Issuer / pull secret | `arm-ca-cluster-issuer` / `far-secret` | `f5-cne-internal-ca` / `far-pull-secret` |
| `USE_GATEWAY_SETTINGS=true` | **absent** | present |

- Withdrawn from the original claim: `storageClassName: nfs` is **not** under
  `spec.dpu`. `dpu:` and `storageClassName:` are both indented two spaces, so
  they're siblings at `spec` level, as in the install doc and the
  [CNEInstance CRD reference](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/cneinstance-crd.html).
- 2.3 comparison: the 2.3.x DPU upgrade guides use the same
  `cnegatewayclass-cr-2.3.x.yaml` / kustomize / `f5-alpha` example. Only the
  version string was bumped.
- Impact: an operator who upgrades using this example never enables
  `USE_GATEWAY_SETTINGS` (see #25).

### 6. Release notes: Debug API link is an unresolved cross-reference

**Verdict:** Confirmed · **Since:** new in 2.4

- [Release notes → Enhancements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#enhancements):
  `see <a class="reference internal" href="#/troubleshooting/debug-apis/spk-debug-apis.md"><code>/troubleshooting/debug-apis/spk-debug-apis.md</code></a>`.
- It renders as monospace text, and its link is a same-page fragment that goes
  nowhere. No `spk-debug-apis` page exists: `troubleshooting/debug-apis/spk-debug-apis.html`
  redirects to the Cloud Docs home page.
- The likely target is [Debug APIs](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/troubleshooting/debug-apis/index.html).
- 2.3 comparison: none of the 2.3.0–2.3.3 release notes contain this link.

### 7. Release notes use two names for the removed gateway CRD

**Verdict:** Corrected (the naming mix is long-standing; the real issue is the inconsistency inside one document) · **Since:** new in 2.4 (as a contradiction)

- [Helm chart and custom resource schema changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#helm-chart-and-custom-resource-schema-changes):
  "Removed the legacy BNKGateway CRD and its GatewayClass parameters reference."
- [Breaking changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#breaking-changes):
  "…serving as an upgraded replacement for F5BnkGateway (Note: F5BnkGateway
  will no longer be valid)."
- Withdrawn from the original claim: "all prior docs call it `F5BnkGateway`".
  The [CRD page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-bnkgateway.html)
  (2.3 and 2.4) is titled "BNKGateway" while its YAML uses `kind: F5BnkGateway`.
  The 2.3.0 notes say "BNKGateway CR"; the 2.3.3 notes say "F5BnkGateway".
- Ask: name the kind (`F5BnkGateway`, `k8s.f5net.com/v1`) and say whether the
  CRD is removed or only unused. It is still installed; see #23.

### 8. Release notes known issue 2455985-1: "crapetemplates" typo

**Verdict:** Confirmed · **Since:** new in 2.4

- [Known issues](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#known-issues)
  (2455985-1): "scrapetemplates.metrics.f5.net → crapetemplates.metrics.f5.com".
  The other two rows of the same issue are spelled correctly, as is the rename
  table in the [Host upgrade guide](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-upgrade-2.3.x-to-2.4.0-using-helm.html#upgrade-the-f5-observer-pod).
- The same known issue also has a stray space in `< UPGRADE VERSION>`.
- 2.3 comparison: 2455985 isn't in any 2.3 release notes; it's a 2.4 rename issue.

### 9. ~~Install a CNE instance: stray `? -->` rendered as text~~

**Verdict:** Withdrawn · **Since:** carried over from 2.3 (the underlying comment)

- The page ([Create Multus network attachment definitions](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-multus-network-attachment-definitions))
  contains a complete, valid HTML comment after the NAD `kubectl apply` step:
  `<!-- TODO: Is the CNEInstance manifest missing storageClassName: <storageClassName>? -->`.
- Browsers don't render it. The original "stray `? -->`" came from a naive
  tag-stripper treating the `>` of `<storageClassName>` as the end of a tag.
- What remains is a leftover author TODO in the published page source,
  identical on 2.3. It's also obsolete, since the 2.4 example already sets
  `storageClassName: nfs`. Cosmetic only; not for the docs-team report.

### 10. Prerequisite versions: Calico, cert-manager and Kubernetes guidance is thin and unchanged from 2.3

**Verdict:** Corrected (applies to the DPU track; the Host track lists no versions) · **Since:** carried over from 2.3

- DPU [System requirements → Software requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#software-requirements):
  `Calico CNI | v3.27.0` and `cert-manager | Latest`.
- The [Host FLO system requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html)
  say only "Calico (vanilla Kubernetes) also supported." and have no
  cert-manager row.
- [Install open-source cert-manager](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/install-and-configure-open-source-cert-manager.html#install-open-source-cert-manager)
  (DPU and Host copies) gives only: "For example, for OpenShift 4.18, the latest
  version of cert-manager that was compatible at the time this was written was
  `v1.19.6`." That's odd on the DPU track, whose index says "The only platform
  supported is vanilla Kubernetes".
- No install page gives Kubernetes 1.35 guidance.
  [Prepare Kubernetes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/prepare-kubernetes.html#add-worker-nodes-via-kubeadm)
  still says the BlueField image installs kubelet/kubeadm 1.30.10.
- Kubernetes 1.35 appears only in the release notes'
  [platform table](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#supported-container-orchestration-platforms),
  whose source carries `<!-- To-Do: Need to be updated -->`.
- 2.3 comparison: the 2.3 system-requirements and cert-manager pages have
  identical text, and the 2.3.3 release notes already listed vanilla k8s 1.35.

### 11. Superseded CRD pages and "Configure the Network" are unchanged from 2.3, with no deprecation note

**Verdict:** Confirmed · **Since:** carried over from 2.3 (unchanged pages)

- [BNKGateway CRD](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-bnkgateway.html#bnkgateway-custom-resource):
  "Note: Using BNKGateway in BIG-IP for Kubernetes is optional."
  [Its sample](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-bnkgateway.html#sample-bnkgateway-cr)
  is `kind: F5BnkGateway`. The page never says deprecated, legacy or "no
  longer", and never mentions GatewaySettings or Infra.
- [F5SPKVlan](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-vlan-crd.html),
  [F5SPKEgress](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-egress-crd.html)
  and [F5SPKStaticRoute](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-static-route-crd.html):
  page text identical to 2.3, with no pointer to Infra or EgressGateway.
- [Configure the Network → Apply internal and external F5SPKVlan CR](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-configure-network.html#apply-internal-and-external-f5spkvlan-cr)
  still has "Apply the VLANs that you have created" with two `kind: F5SPKVlan`
  manifests. It's still the first entry of the network section, ahead of
  "Overview of network infrastructure CRDs".
- Nuance: the [breaking changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#breaking-changes)
  say "no longer valid" only for F5BnkGateway. For the others they say Infra
  "consolidates F5SPKVlan, F5SPKStaticRoute, VRF, and VxLAN CRDs" and
  EgressGateway is "designed to replace F5SPKEgress". "Superseded" is the
  accurate word; the CRDs are still installed (#23).
- Side finding: the [SPK custom resources index](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-custom-resources.html)
  labels a link "BNKGateway - Enables the creation of complex IP address
  lists…", but it points to the F5BigCneAddressList page.

### 12. NetPolicy CRD page: wrong YAML under the iRule step, mismatched names

**Verdict:** Confirmed · **Since:** new in 2.4 (regression; the 2.3 page was correct)

- [NetPolicy with F5BigCneIrule attached to Gateway](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-NetPolicy.html#netpolicy-with-f5bigcneirule-attached-to-gateway):
  under "Apply F5BigCneIrule CR" the YAML is `kind: F5BigPersistenceProfile`
  (`name: persistence-profile-one`, `persistenceType: MODEL_CONTEXT_PROTOCOL`).
  The output right below says `f5bigcneirule.k8s.f5net.com/irule-one created`.
- [NetPolicy with F5BigPersistenceProfile attached to Gateway](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-NetPolicy.html#netpolicy-with-f5bigpersistenceprofile-attached-to-gateway):
  - The NetPolicy still references `kind: F5BigCneIrule` / `name: irule-one`,
    never the persistence profile.
  - `metadata.name` is `my-test-netpolicy`, but the output shows
    `NetPolicy.gateway.k8s.f5.com/persistence-netpolicy created`.
- 2.3 comparison: the [2.3 BNKNetPolicy page](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/custom-resource-definitions/bnk-bnkNetPolicy.html#bnknetpolicy-with-f5bigcneirule-attached-to-gateway)
  showed a correct `kind: F5BigCneIrule` with an `iRule:` body and had no
  persistence section.

### 13. Global BGP routing how-to: neighbor Secret has an F5 apiVersion

**Verdict:** Confirmed (and also present on a second page) · **Since:** new in 2.4 (the page doesn't exist in 2.3)

- [Configure global BGP routing → Procedure](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/configure-global-BGP-routing.html#procedure):
  "Create a BGP neighbor secret." with `apiVersion: k8s.f5net.com/v1alpha1` /
  `kind: Secret`.
- The same wrong Secret appears twice on
  [BGP sample files by use case](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/border-gateway-protocol/sample-files-by-use-case.html),
  each wrapped in a literal "```yaml" fence that leaked into the page.
- On a live 2.4.0 cluster, `k8s.f5net.com/v1alpha1` serves `GlobalRoutingConfig`
  and `RoutingTemplate` but no Secret. It must be core `apiVersion: v1`, or
  `kubectl apply` fails.

### 14. "Network configuration and application traffic management" page: wrong API groups, fields and versions

**Verdict:** Corrected (several sub-points tightened) · **Since:** new in 2.4 (the page doesn't exist in 2.3)

Page: [network-configuration-and-application-traffic-management.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html).
Ground truth is the 2.4 CRD reference pages and the CRDs on a live 2.4.0 cluster.

- **L4Route** ([first "iRules integration (NetPolicy)" section](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#irules-integration-netpolicy)):
  the example uses `apiVersion: k8s.f5.com/v1` with
  `host: 11.19.1.100` / `port: 8050` / `pool.service`.
  - The real CRD is `gateway.k8s.f5.com/v1` with `parentRefs` and `rules`
    ([L4Route CRD page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-l4route.html#basic-l4route-and-gateway-api-crs)).
    The live `l4routes.gateway.k8s.f5.com` schema has no `host`, `port` or `pool`.
- **NetPolicy** ([second "iRules integration (NetPolicy)" section](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#id1)):
  `apiVersion: k8s.f5.com/v1`, singular `targetRef:`, `irules: - name: my-irule`.
- **SecPolicy** ([Firewall policy](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#firewall-policy-f5bigfwpolicy)):
  `apiVersion: k8s.f5.com/v1`, singular `targetRef:`, `firewallPolicies:`.
  (The original claim said `irules:` for both; SecPolicy uses `firewallPolicies:`.)
  - The real CRDs are `gateway.k8s.f5.com/v1alpha1` with `targetRefs[]` and
    `extensionRefs[]`
    ([NetPolicy](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-NetPolicy.html),
    [SecPolicy](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-SecPolicy.html)).
    On the live cluster `group`/`kind`/`name` are required in both arrays.
- **F5BigCneIrule**: the page uses `spec.irule`. The CRD field is `spec.iRule`,
  and it's required
  ([iRule CRD page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-irule-in-gatewayapi.html#sample-f5bigcneirule-cr),
  live schema `required: ["iRule"]`). The page's group `k8s.f5net.com/v1` is
  correct.
- **Gateway API version**:
  - Wrong: [Gateway API setup](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#gateway-api-setup)
    says "BNK supports Gateway API v1.5.0". 2.4.0 ships the v1.4.1
    experimental-channel CRDs, and the controller runs with
    `GATEWAY_API_VERSION=v1.4.1`.
  - Stale: the SPK→BNK migration appendix's "Key differences" table says
    "v1.2.0 with SecPolicy and NetPolicy".
  - Consistent with what ships:
    [System requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#minimum-gateway-api-version)
    ("v1.4.1 experimental or later"),
    [Gateway API page](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-gateway-api.html#gateway-api-crds)
    ("features from v1.4.0") and the HTTPRoute how-to (#22).
- **Duplicate heading**: "iRules integration (NetPolicy)" appears twice
  (`#irules-integration-netpolicy` holds the L4Route example, `#id1` the real
  iRule and NetPolicy).
- **"Trust the well-known CA set"**
  ([section](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#trust-the-well-known-ca-set)):
  the code block contains only the literal text "```yaml", a leaked Markdown
  fence with no YAML. (The original said "empty".)
- **BackendTLSPolicy**: [Secure connections to backends](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#secure-connections-to-backends)
  shows `gateway.networking.k8s.io/v1alpha2` with `targetRefs[].port`, while
  three later examples use `/v1`. The live cluster serves `v1` and `v1alpha3`
  only; `v1alpha2` doesn't apply.
- **Migration appendix** ([Migration steps](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/network-configuration-and-application-traffic-management.html#migration-steps)):
  "SPK networking CRs (F5SPKVlan, F5SPKStaticRoute, and others) are compatible
  with BNK and do not need format changes" and "Reapply exported CRs.
  Networking CRs should apply without changes."
  - Taken narrowly, F5SPKVlan and F5SPKStaticRoute still apply on 2.4.0
    (the CRDs are installed, #23).
  - The hard contradiction is the export list: it includes L4Route, whose API
    group "has changed to gateway.k8s.f5.com" (breaking changes), so a 2.3
    L4Route fails to apply (#23). F5SPKEgress is "designed to be replaced" by
    EgressGateway.

### 15. GatewaySettings how-to: "Complete example" YAML is flattened

**Verdict:** Corrected (worse than stated) · **Since:** new in 2.4 (the page doesn't exist in 2.3)

- [Complete example](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-tenant-traffic-settings-with-gatewaysettings.html#complete-example)
  (text taken from the page's `<pre>` block):
  ```
  metadata:
  name: my-gateway-settings
  namespace: f5-gateways
  spec:
  sourceNATPools:
    - name: egress-snat
  ...
  ingressConfig:
  ...
  egressConfigs:
  ```
- `name`/`namespace` and the spec keys (`sourceNATPools`, `ingressConfig`,
  `egressConfigs`) are all at column 0. It still parses as YAML, but
  `metadata` and `spec` come out null, so `kubectl apply` rejects the object.
- Working reference: ocibnkctl's
  `internal/scenarios/ficdynamicip/manifests/02-gatewaysettings.yaml`
  (green on 2.4.0).

### 16. Infra how-to: step numbering

**Verdict:** Corrected · **Since:** new in 2.4 (the page doesn't exist in 2.3)

- [Create the Infra CR](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-network-infrastructure-with-infra-crd.html#create-the-infra-cr)
  has six steps: "Define IPAM pools", "Define network attachments",
  "Configure VRFs (optional)", "Step 4: Define networks", "Add static routes
  (optional)", "Apply the Infra CR".
- Each is its own one-item ordered list, so every step renders as "1.". Only
  the fourth carries a literal "Step 4:" prefix.

### 17. Helm version guidance contradicts itself

**Verdict:** Confirmed (understated originally) · **Since:** carried over from 2.3

- [System requirements](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/prepare-your-computer-and-cluster/system-requirements.html#software-requirements):
  `Helm | v3.x`.
- The [release notes platform table](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#supported-container-orchestration-platforms)
  ("Recommended Helm version") gives Helm **4.1** for vanilla k8s 1.35 and
  OpenShift 4.22, and 3.17–3.19 for older platforms.
- [Prepare your computer](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-cnfs-self-managed-helm/prepare-your-computer-and-cluster/prepare-your-computer.html)
  (Host/Helm) says: "Helm 3.8.0 through 3.20.x … The f5-cert-manager chart in
  F5 BIG-IP Cloud-Native Edition isn't currently compatible with Helm 4.0 or
  later."
- The [2.4.0→2.3.x rollback guide](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-rollback-2.4.0-to-2.3.x-using-helm.html)
  says: "Helm rollback does not work for CWC if you are using Helm 4.0 or later
  versions."
- So the recommended Helm 4.1 is declared incompatible elsewhere in the same
  docs set.
- 2.3 comparison: the 2.3.3 release notes already recommended Helm 4.1 for
  k8s 1.35, and the 2.3 pages already had the "v3.x" and "not compatible with
  Helm 4.0" text.

### 18. Proxy Protocol how-to: wrong L4Route group, a non-working iRule, and incomplete NetPolicy refs

**Verdict:** Confirmed (and understated) · **Since:** carried over from 2.3 (L4Route group, iRule); NetPolicy refs new in 2.4

- [Step 3: Create the L4 route](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html#step-3-create-the-l4-route)
  uses `apiVersion: gateway.networking.k8s.io/v1`. L4Route is an F5 CRD
  (`gateway.k8s.f5.com/v1`); no `l4routes` resource exists in the upstream
  group on a live cluster. The [2.3 how-to](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/how-tos/proxy-protocol.html)
  had the same wrong group.
- [Step 1: Create the iRule](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html#step-1-create-the-irule):
  - Inside `if {$version eq 4}`, the `encode` proc computes
    `[string length $ipv6_compressed]` but never sets `$ipv6_compressed`, so it
    raises a Tcl error.
  - The proc is also truncated: `proxy_addr` is only appended to, the signature
    and addresses are never encoded, there is no `return`, and nothing handles
    version 6. Even without the error, `call encode` returns nothing.
- [Step 4: Apply the policy](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/proxy-protocol.html#step-4-apply-the-policy):
  the NetPolicy's `extensionRefs` and `targetRefs` omit `group`/`kind`, which
  the 2.4.0 CRD requires. This is the only real change from 2.3, where it was
  a BNKNetPolicy.
- Working reference: ocibnkctl's `proxy-protocol-l4` scenario
  (`internal/scenarios/proxyprotocol/manifests/05-irule.yaml`,
  `06-l4route.yaml`, `07-bnknetpolicy.yaml`). It uses its own PROXY v1 iRule
  (`TCP::respond "PROXY TCP4 …"` on `SERVER_CONNECTED`),
  `gateway.k8s.f5.com/v1`, and full `group`/`kind` refs. Green on 2.4.0.

### 19. External-resource LB how-to: internal staging URLs and an undefined pool name

**Verdict:** Corrected (the staging URLs are plain text, not hyperlinks) · **Since:** carried over from 2.3 verbatim

- [Configure external-resource load balancing](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html)
  prints two bare-text URLs to the internal staging host `clouddocs.f5networks.net`:
  - [Before you begin](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html#before-you-begin):
    "Refer F5 documentation about Static Routes:
    `https://clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/custom-resource-definitions/spk-static-Route-crd.html`".
    It also points at the SPK static-route CRD, which Infra supersedes in 2.4.
  - End of page: "For more information about configuring route rule matching,
    see: `https://clouddocs.f5networks.net/bigip-next-for-kubernetes/rananth-techdocs-4462/how-tos/Configure-HTTP-traffic-steering-with-Gateway-API-HTTPRoute.html`".
- [Set up a Pool CR with external endpoints](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/configure-external-resource-load-balancing.html#set-up-a-pool-cr-with-external-endpoints)
  names the Pool `infra-backend-pool`. Both HTTPRoute examples reference
  `backendRefs: - name: http-pool` (`kind: Pool`, `group: k8s.f5net.com`), which
  is defined nowhere on the page.
- 2.3 comparison: the [2.3 page](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/how-tos/configure-external-resource-load-balancing.html)
  is identical.

### 20. Gateway CRD page: `parametersRef` table still describes F5BnkGateway

**Verdict:** Confirmed · **Since:** new in 2.4 (the examples were updated, the table wasn't)

- [CR parameters](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-gateway.html#cr-parameters):
  "infrastructure.parametersRef … CNE controller support F5BnkGateway resource
  here". The examples given are group `k8s.f5net.com`, kind `F5BnkGateway`,
  name `f5-bnkgateway`.
- Every sample on the same page uses `group: gateway.k8s.f5.com` /
  `kind: GatewaySettings`, e.g.
  [Gateway CR with dynamic IP address assigned through GatewaySettings](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/bnk-gateway-api-gateway.html#gateway-cr-with-dynamic-ip-address-assigned-through-gatewaysettings).
- 2.3 comparison: the [2.3 page](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/custom-resource-definitions/bnk-gateway-api-gateway.html#cr-parameters)
  had the same table, but its examples also used F5BnkGateway, so it was
  consistent.
- Also on the 2.4 page: the parameter table is rendered twice, and the
  "Overview" heading is duplicated (`#overview`, `#id1`).

### 21. `USE_GATEWAY_SETTINGS` is not documented anywhere except one install example

**Verdict:** Corrected (the CRD page documents no component env var at all, so its silence alone is weak evidence) · **Since:** new in 2.4

- The [CNEInstance CRD reference](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/cneinstance-crd.html)
  doesn't mention `USE_GATEWAY_SETTINGS`. Its `spec.advanced.cneController.env`
  table is generic (`name`, `value`, `valueFrom.*`, `maxItems 50`) and names no
  env var for any component, not even `TMM_DEFAULT_MTU`.
- Across the whole 2.4 site crawl (177 pages) the variable appears on exactly
  one page, the DPU [Install a CNE instance using FLO](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads):
  ```yaml
      cneController:
        env:
        - name: TMM_DEFAULT_MTU
          value: "9000"
        - name: "USE_GATEWAY_SETTINGS"
          value: "true"
  ```
  Diffing against the [2.3 version of that page](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/install/bnk-dpu-self-managed-flo/install/install-a-cne-instance-using-flo.html#create-a-manifest-for-the-cne-instance-workloads),
  those two lines are the only content change.
- It is **not** on any of these:
  - [Host FLO install](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-self-managed-flo/install/install-a-cne-instance-using-flo.html)
    (`cneController.env` has only `TMM_DEFAULT_MTU`), the closest match to
    ocibnkctl's shape
  - [Host Helm install](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-host-cnfs-self-managed-helm/install/install-a-cne-instance-using-helm.html)
    (no `cneController` block)
  - the DPU upgrade guide's CNEInstance (#5)
  - the release notes
  - the GatewaySettings and Infra CRD pages and how-tos
- Nothing tells a reader the variable exists, what it does, or whether it's
  required. See #25 for what it does at runtime.

### 22. HTTPRoute how-to: Gateway API "version 1.4"

**Verdict:** Corrected (this page is consistent with what ships; the outlier is #14's v1.5.0) · **Since:** carried over from 2.3

- [HTTPRoute how-to → Overview](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/Configure-HTTP-traffic-steering-with-Gateway-API-HTTPRoute.html#overview):
  "part of the Gateway API standard (version 1.4)". The conformance link
  points at `tree/v1.4.0`.
- That matches the v1.4.1 CRDs 2.4.0 ships. The inconsistency is the traffic
  page's "v1.5.0" and the migration appendix's "v1.2.0" (#14). Not a separate
  error; kept for numbering.

### 23. Release notes vs shipped CRDs: "removed" CRDs still installed, "renamed" groups actually removed

**Verdict:** Confirmed on a live 2.4.0 cluster (2026-09-14, fresh FLO install) · **Since:** new in 2.4

- What the release notes say
  ([breaking changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#breaking-changes),
  [schema changes](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/release-notes-bnk-2.4.0.html#helm-chart-and-custom-resource-schema-changes)):
  - "F5BnkGateway will no longer be valid"
  - "Removed the legacy BNKGateway CRD…"
  - "BNKSecPolicy and BNKNetPolicy have been renamed to SecPolicy and NetPolicy"
  - "The API group gateway.k8s.f5net.com has changed to gateway.k8s.f5.com."

  Nothing says the old group or CRDs are removed, or that 2.3 manifests will fail.
- **Still installed** by the 2.4.0 crd-installer (`crd-installer:v14.91.12-0.4.7`,
  all labelled `app.kubernetes.io/version: 14.91.12-0.4.7`, all versions
  `served: true`, no `deprecated` flag):
  - `f5-bnkgateways.k8s.f5net.com`
  - `f5-spk-vlans.k8s.f5net.com`
  - `f5-spk-egresses.k8s.f5net.com`
  - `f5-spk-egresssips.k8s.f5net.com`
  - `f5-spk-staticroutes.k8s.f5net.com`
  - also `f5-spk-snatpools.k8s.f5net.com`
- **Removed outright:** there are no CRDs left in `gateway.k8s.f5net.com`
  (`kubectl api-resources --api-group=gateway.k8s.f5net.com` is empty). Their
  replacements are `l4routes.gateway.k8s.f5.com` (v1) and
  `netpolicies.gateway.k8s.f5.com` / `secpolicies.gateway.k8s.f5.com` (v1alpha1).
- A 2.3-style L4Route (the exact group/version ocibnkctl's 2.3 manifests used,
  e.g. `release/2.3.2:internal/scenarios/tcpl4lb/manifests/05-l4route.yaml`)
  fails even a server-side dry-run:
  ```
  error: resource mapping not found for name: "errata-probe" namespace: "default" from "STDIN":
  no matches for kind "L4Route" in version "gateway.k8s.f5net.com/v1"
  ensure CRDs are installed first
  ```
- The controller no longer needs an F5BnkGateway. A Gateway without
  `infrastructure.parametersRef` is programmed from `spec.addresses`
  (controller log: "Gateway …: FIC deployed but no infrastructureRef, using
  Spec.Addresses"), observed with `USE_GATEWAY_SETTINGS=true` set.
- Upgrade impact: after an upgrade, some 2.3 resources fail to apply (L4Route,
  BNKNetPolicy/BNKSecPolicy). Others still apply but are superseded
  (F5BnkGateway, F5SPKVlan, F5SPKStaticRoute, F5SPKEgress). The notes don't
  say which is which.
  - Neither [DPU](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-dpu-upgrade-2.3.x-to-2.4.0-using-flo.html)
    nor [Host](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/releases/bnk-host-upgrade-2.3.x-to-2.4.0-using-helm.html)
    upgrade guide mentions Infra, GatewaySettings, EgressGateway, NetPolicy or
    the API-group change.
  - The only rename table is for the Observer CRDs (`metrics.f5.net → metrics.f5.com`).
- Internal note: ocibnkctl's `validate` (`internal/poc/validate.go`) says
  F5SPKVlan was "removed" in 2.4.0. The accurate wording is that the kind is
  superseded by Infra while the CRD remains installed.

### 24. FIC for Gateway API page moved without a redirect; the Use cases section is empty

**Verdict:** Corrected (the content moved, it wasn't deleted; the Gateway API page's link was already broken in 2.3) · **Since:** new in 2.4 (move); link breakage carried over from 2.3

- The 2.3 URL [use-cases/bnk-ficforgatewayapi.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/use-cases/bnk-ficforgatewayapi.html)
  returns HTTP 301 to the generic Cloud Docs home page on the 2.4 site. The
  [2.3 copy](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/use-cases/bnk-ficforgatewayapi.html)
  still returns 200 ("Dynamic IP address allocation"). ocibnkctl's
  `fic-dynamic-ip` scenario links to it.
- The content moved to
  [components/bnk-ficforgatewayapi.html](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/components/bnk-ficforgatewayapi.html)
  ("Dynamic IP address allocation", rewritten for Infra/GatewaySettings,
  linked from [Components](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/components/index.html)).
  The old URL doesn't redirect there, and the new page has its own broken
  cross-reference (`href="#custom-resource-definitions/bnk-GatewaySettings.md"`).
- The [2.4 Use cases index](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/use-cases/index.html)
  is empty ("In this section" lists nothing). The original claim that it
  "links only to the Vxlan CRD page" was wrong: Vxlan appears only as Sphinx's
  `<link rel="prev">` in the page head.
- Separate, older problem: the
  [Gateway API page → Using F5 IPAM Controller](https://clouddocs.f5.com/bigip-next-for-kubernetes/latest/install/bnk-dpu-self-managed-flo/network/bnk-gateway-api.html#using-f5-ipam-controller)
  says "See F5 IPAM Controller for Gateway API", but the link is an unresolved
  cross-reference (`href="#/how-tos/bnk-ficforgatewayapi.md"`) that never
  pointed at `use-cases/…`. The
  [2.3 Gateway API page](https://clouddocs.f5.com/bigip-next-for-kubernetes/2.3/install/bnk-dpu-self-managed-flo/network/bnk-gateway-api.html#using-f5-ipam-controller)
  has the same broken link.

### 25. `USE_GATEWAY_SETTINGS`: what it actually does on a 2.4.0 install

**Verdict:** Confirmed on a live 2.4.0 cluster by removing and restoring the variable (2026-09-14) · **Since:** new in 2.4

**Docs side** (see #21): only the DPU FLO install example sets it. The Host
FLO/Helm install pages, the DPU upgrade guide, the release notes and the
CNEInstance CRD reference don't mention it.

**Runtime side.** Test setup:

- Cluster: freshly created with `ocibnkctl e2e`, 1 control node plus 1 TMM
  worker, BNK 2.4.0, f5-cne-controller `v14.91.12-0.4.7`.
- The only change between phases was the `USE_GATEWAY_SETTINGS` entry in
  `CNEInstance.spec.advanced.cneController.env`. FLO propagates it to the
  `CNEController` CR and the `f5-cne-controller` Deployment within about
  10 seconds.

| Time (UTC) | Step | Observed |
|---|---|---|
| 14:44:52 | `deploy cne` creates Infra (flag **on**) | Infra `Accepted`/`ResolvedRefs`/`Programmed=True` |
| 14:48:12 | Flag removed from CNEInstance | Controller rolled out at 14:48:22. The only `GATEWAY*` env var left is `GATEWAY_API_VERSION=v1.4.1`, and the CNEController CR carries none. **FLO sets nothing by default.** |
| 14:49:01 | Created GatewaySettings `probe-gs` (IPAM ref `bnk-dynamic-vips`), Gateway `probe-dyn-gw` (no addresses, `parametersRef` → `probe-gs`), Gateway `probe-static-gw` (`spec.addresses` 203.0.113.130) | Static Gateway `Programmed=True` on 203.0.113.130 immediately (log: "FIC deployed but no infrastructureRef, using Spec.Addresses") |
| 14:49:01 | `kubectl delete infra infra` | **Deletion hangs.** Finalizer `handletmmconfig_inconsistency` is never removed, and the stale status still reads `Programmed=True` |
| 14:55:00 (+6 min) | Snapshot | `probe-gs`: `Accepted=Unknown`, reason `Pending`, "Waiting for controller", `lastTransitionTime: 1970-01-01T00:00:00Z` (the CRD default; the controller never wrote status). `probe-dyn-gw`: `Programmed=False` "Referenced GatewaySettings "probe-gs" not found or is being deleted", no address. Controller log: "Referenced GatewaySettings not found, preserving existing TMM config", "No IPAM found for Gateway: probe-dyn-gw" |
| 14:55:01 | Finalizer removed manually; fresh Infra created (same spec ocibnkctl renders) | — |
| 14:59:01 (+4 min) | Snapshot | Fresh Infra: `Accepted=Unknown`, `Pending`, "Waiting for controller", `lastTransitionTime` 1970. No controller log line mentions Infra or GatewaySettings in that window |
| 14:59:01 | Flag restored (`USE_GATEWAY_SETTINGS=true`) | — |
| 14:59:25 | (+24 s) | Infra and `probe-gs`: `Accepted`/`ResolvedRefs`/`Programmed=True` |
| 14:59:31 | (+30 s) | `probe-dyn-gw` `Programmed=True`, address **203.0.113.110** allocated from the Infra IPAM pool |

Conclusions:

- **Without the flag, the 2.4.0 CNE controller doesn't reconcile Infra or
  GatewaySettings at all.** New objects stay "Pending: Waiting for controller"
  (checked at 4 and 6 minutes) with the CRD's default status. A Gateway that
  references a GatewaySettings gets no address and reports the GatewaySettings
  as "not found".
- Deleting an existing Infra hangs on its finalizer until the flag is back.
- With the flag, the same objects reconcile within about 25 seconds.
- **Static-address Gateways work either way.**
- A default FLO install (a CNEInstance without the override, like the Host
  FLO/Helm install pages and the DPU upgrade guide) runs the controller without
  the flag. On such an install, the Infra/GatewaySettings model that the 2.4.0
  release notes introduce as the replacement for F5BnkGateway and the F5SPK*
  network CRs silently does nothing.
- "Waiting for controller" is also the normal initial state for a few seconds
  to minutes (e.g. while the Infra quota populates on a fresh deploy). Here it
  was held for 4 to 6 minutes and cleared within 25 seconds of restoring the
  flag.
