package poc

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// ValidationResult collects all schema-level problems found in a
// poc.yaml. The CLI prints the full list rather than short-circuiting
// on the first error so an operator gets one round-trip of edits per
// `validate` invocation.
type ValidationResult struct {
	Errors   []string
	Warnings []string
}

func (v ValidationResult) Valid() bool { return len(v.Errors) == 0 }

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

// Validate runs every schema-level check. References to keys/ files are
// verified separately by the CLI (it needs the PoC repo path).
func (p *PoC) Validate() ValidationResult {
	var r ValidationResult

	if p.APIVersion != APIVersion {
		r.Errors = append(r.Errors, fmt.Sprintf("apiVersion %q must equal %q", p.APIVersion, APIVersion))
	}
	if p.Kind != Kind {
		r.Errors = append(r.Errors, fmt.Sprintf("kind %q must equal %q", p.Kind, Kind))
	}
	if p.Metadata.Name == "" {
		r.Errors = append(r.Errors, "metadata.name: required")
	} else if !nameRE.MatchString(p.Metadata.Name) {
		r.Errors = append(r.Errors, fmt.Sprintf("metadata.name %q: must match [a-z0-9][a-z0-9-]{0,30}[a-z0-9]", p.Metadata.Name))
	}
	if p.Metadata.BNKVersion == "" {
		r.Errors = append(r.Errors, "metadata.bnk_version: required")
	}

	if p.Cluster.Name == "" {
		r.Errors = append(r.Errors, "cluster.name: required")
	}
	switch p.Cluster.Provider {
	case "docker", "podman":
		// ok
	case "":
		r.Errors = append(r.Errors, "cluster.provider: required (docker | podman)")
	default:
		r.Errors = append(r.Errors, fmt.Sprintf("cluster.provider %q: must be docker or podman", p.Cluster.Provider))
	}
	if p.Cluster.TMMNodes < 0 || p.Cluster.TMMNodes > MaxTMMNodes {
		r.Errors = append(r.Errors, fmt.Sprintf("cluster.tmm_nodes %d: must be 0 (=1) to %d", p.Cluster.TMMNodes, MaxTMMNodes))
	}

	if p.Versions.K8s == "" {
		r.Errors = append(r.Errors, "versions.k8s: required")
	}
	if p.Versions.NodeImage == "" {
		r.Errors = append(r.Errors, "versions.node_image: required (e.g. rancher/k3s:v1.30.14-k3s1)")
	}
	if p.Versions.CNEManifest == "" {
		r.Errors = append(r.Errors, "versions.cne_manifest: required")
	}

	// Networks is now optional — older PoCs pre-populated
	// bnk-internal / bnk-external docker bridges but `cluster up`
	// no longer creates them. If the user explicitly sets a name
	// and subnet, still validate CIDR shape.
	for _, n := range []struct {
		label string
		net   DockerNetwork
	}{
		{"networks.internal", p.Networks.Internal},
		{"networks.external", p.Networks.External},
	} {
		if n.net.Name == "" && n.net.Subnet == "" {
			continue
		}
		if n.net.Subnet != "" {
			if _, _, err := net.ParseCIDR(n.net.Subnet); err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("%s.subnet %q: %v", n.label, n.net.Subnet, err))
			}
		}
	}
	if p.Networks.Internal.Name != "" && p.Networks.Internal.Name == p.Networks.External.Name {
		r.Errors = append(r.Errors, "networks.internal.name and networks.external.name must differ")
	}

	if p.BNK.FARKeyRef == "" {
		r.Errors = append(r.Errors, "bnk.far_key_ref: required (path to FAR tgz under the PoC repo)")
	}
	if p.BNK.JWTRef == "" {
		r.Errors = append(r.Errors, "bnk.jwt_ref: required (path to JWT under the PoC repo)")
	}
	if !p.BNK.DemoMode {
		r.Warnings = append(r.Warnings, "bnk.demo_mode is false: TMM will require SR-IOV / DPU-backed interfaces, which the k3s demo shape cannot provide. Set it true for this deployment.")
	}
	switch p.BNK.HostProfile {
	case "", HostProfileStandard, HostProfileSmall:
		// ok
	default:
		r.Errors = append(r.Errors, fmt.Sprintf("bnk.host_profile %q: must be %q or %q", p.BNK.HostProfile, HostProfileStandard, HostProfileSmall))
	}
	if p.BNK.IsSmallHost() {
		r.Warnings = append(r.Warnings, "bnk.host_profile=small: TMM metrics subsystem (observer sidecar) is disabled so TMM fits a 4-core node; e2e auto-runs `deploy shrink` to cap the remaining pods.")
	}

	// TMM data-plane mode: validate the enum value and reconcile it with
	// the legacy tmm_active_active bool. The two all-active modes are
	// mutually exclusive on net1 (selfip-dag needs mapres TRUE, anycast-bgp
	// needs FALSE), so we reject any poc.yaml that asks for both at once.
	switch p.BNK.TMMDataplaneMode {
	case "", DataplaneStandby, DataplaneSelfIPDAG, DataplaneAnycastBGP:
		// ok
	default:
		r.Errors = append(r.Errors, fmt.Sprintf(
			"bnk.tmm_dataplane_mode %q: must be %q, %q, or %q",
			p.BNK.TMMDataplaneMode, DataplaneStandby, DataplaneSelfIPDAG, DataplaneAnycastBGP))
	}
	// Legacy bool vs new field: if both are set they must agree. The only
	// meaning of `tmm_active_active: true` is selfip-dag, so it conflicts
	// with any explicit mode that isn't selfip-dag.
	if p.BNK.ActiveActive && p.BNK.TMMDataplaneMode != "" &&
		p.BNK.TMMDataplaneMode != DataplaneSelfIPDAG {
		r.Errors = append(r.Errors, fmt.Sprintf(
			"bnk.tmm_active_active: true conflicts with bnk.tmm_dataplane_mode: %q — "+
				"the legacy bool means %q; drop tmm_active_active and use tmm_dataplane_mode alone",
			p.BNK.TMMDataplaneMode, DataplaneSelfIPDAG))
	}
	if p.BNK.ActiveActive && p.BNK.TMMDataplaneMode == "" {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"bnk.tmm_active_active is deprecated: it aliases to tmm_dataplane_mode: %q — "+
				"prefer setting tmm_dataplane_mode explicitly.", DataplaneSelfIPDAG))
	}
	if p.BNK.IsAnycastBGP() {
		r.Warnings = append(r.Warnings,
			"bnk.tmm_dataplane_mode=anycast-bgp: real cross-node ECMP fan-out needs a "+
				"shared-L2 underlay + an upstream ToR receiving all TMM sessions — on a single host "+
				"the per-node bnk-bgp bridges are isolated, so the demo validates the anycast MODEL "+
				"(each TMM advertises its VIP /32 to a co-located peer), not multi-node fan-out.")
	}

	if p.BNKForge.Enabled {
		if p.BNKForge.URL != "" && !strings.HasPrefix(p.BNKForge.URL, "http") {
			r.Errors = append(r.Errors, fmt.Sprintf("bnk_forge.url %q: must start with http or https", p.BNKForge.URL))
		}
	}

	return r
}
