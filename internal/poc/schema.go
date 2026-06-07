// Package poc defines the on-disk schema for a ocibnkctl PoC repo
// (poc.yaml). poc.yaml is the source of truth — tear-down and redeploy
// read only this file. Anything not captured here is not part of the
// PoC.
package poc

import "time"

const (
	APIVersion = "ocibnkctl.f5.com/v1alpha1"
	Kind       = "PoC"
	FileName   = "poc.yaml"
)

type PoC struct {
	APIVersion string    `yaml:"apiVersion"`
	Kind       string    `yaml:"kind"`
	Metadata   Metadata  `yaml:"metadata"`
	Versions   Versions  `yaml:"versions"`
	Cluster    Cluster   `yaml:"cluster"`
	Networks   Networks  `yaml:"networks"`
	BNK        BNK       `yaml:"bnk"`
	BNKForge   BNKForge  `yaml:"bnk_forge,omitempty"`
	Status     Status    `yaml:"status"`
}

type Metadata struct {
	Name              string    `yaml:"name"`
	Customer          string    `yaml:"customer,omitempty"`
	Created           time.Time `yaml:"created"`
	OcibnkctlVersion  string    `yaml:"ocibnkctl_version,omitempty"`
	BNKVersion        string    `yaml:"bnk_version"`
}

// Versions captures the BNK 2.3.0 component pins. FLOChart is resolved
// from the release-manifest chart at deploy time — empty here means
// "not yet resolved".
type Versions struct {
	K8s         string `yaml:"k8s"`
	NodeImage   string `yaml:"node_image"`
	FLOChart    string `yaml:"flo_chart,omitempty"`
	CNEManifest string `yaml:"cne_manifest"`
}

// Cluster is the cluster shape. Topology is hard-coded at two
// nodes (one combined control-plane+worker, one worker labelled for
// TMM) — the only knobs an operator turns are the cluster name and
// which container runtime the k3s nodes run on (docker vs podman).
type Cluster struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"` // "docker" or "podman"
}

// Networks are the two docker bridge networks attached to both k3s
// node containers. They exist as "scenery" — a routable space where
// operators can spin up test clients, routers, or upstreams alongside
// TMM. TMM itself runs in demo mode and uses virtio inside its pod
// netns, so it does not attach to these networks.
type Networks struct {
	Internal DockerNetwork `yaml:"internal"`
	External DockerNetwork `yaml:"external"`
}

type DockerNetwork struct {
	Name   string `yaml:"name"`
	Subnet string `yaml:"subnet"`
}

type BNK struct {
	FARKeyRef string `yaml:"far_key_ref"`
	JWTRef    string `yaml:"jwt_ref"`
	// DemoMode toggles CNEInstance.advanced.demoMode.enabled. Without
	// it, TMM expects SR-IOV / DPU-backed interfaces; with it, TMM
	// uses virtio inside the pod netns — the only mode that works in
	// this shape. Default true; leaving it false is an explicit choice
	// the operator makes.
	DemoMode bool `yaml:"demo_mode"`
	// TMM node label: ocibnkctl labels the k3s worker container
	// `app=f5-tmm` so TMM nodeSelector lands TMM there. The label is
	// hard-coded matching f5-bnk-udf convention; only surfaced here
	// so an operator who wants to override it (e.g. test multi-node)
	// can do so without recompiling.
	TMMNodeLabelKey   string `yaml:"tmm_node_label_key,omitempty"`
	TMMNodeLabelValue string `yaml:"tmm_node_label_value,omitempty"`
	// HostProfile selects a resource profile for the deployment.
	//
	//   "" / "standard" — the full BNK 2.3 footprint (10-core / 16 GB floor).
	//   "small"         — a Raspberry-Pi-class 4-core / 16 GB host. Disables
	//                     CNEInstance.telemetry.metricSubsystem, which drops
	//                     TMM's observer/tmStats sidecar (-700m) so the TMM
	//                     pod (4.1c stock) falls to ~3.4c and fits a single
	//                     4-core node — the f5-tmm-pod-manager enforces TMM's
	//                     per-container resource VALUES (no override survives)
	//                     but honors a smaller container SET via this flag.
	//                     Also lowers the `doctor` core floor. Pair with
	//                     `ocibnkctl deploy shrink` to cap every other pod.
	HostProfile string `yaml:"host_profile,omitempty"`
}

// Host profile values for BNK.HostProfile.
const (
	HostProfileStandard = "standard"
	HostProfileSmall    = "small"
)

// IsSmallHost reports whether the PoC targets a small (4-core/16GB) host.
func (b BNK) IsSmallHost() bool { return b.HostProfile == HostProfileSmall }

// ResolveHostProfile resolves the configured host_profile against the host's
// core count. An explicit "small" or "standard" is always honored. An unset
// profile is treated as "auto": it resolves to "small" when the host has
// fewer than stdFloor cores (so TMM sheds its metrics sidecar and fits a
// 4-core node), otherwise "standard". The autoSmall bool reports whether the
// small profile was applied automatically rather than configured — the caller
// uses it to log the decision. This mirrors the auto deploy-shrink rule so a
// tight host needs no hand-edited poc.yaml.
func (b BNK) ResolveHostProfile(cores, stdFloor int) (profile string, autoSmall bool) {
	switch b.HostProfile {
	case HostProfileSmall:
		return HostProfileSmall, false
	case HostProfileStandard:
		return HostProfileStandard, false
	default: // "" — auto
		if cores < stdFloor {
			return HostProfileSmall, true
		}
		return HostProfileStandard, false
	}
}

// MetricSubsystemEnabled is the value for
// CNEInstance.spec.telemetry.metricSubsystem.enabled. The small-host
// profile disables it to shed TMM's observer sidecar; everything else
// keeps the metrics pipeline on.
func (b BNK) MetricSubsystemEnabled() bool { return !b.IsSmallHost() }

// BNKForge mirrors dpubnkctl's bnk_forge block. Auto-populated by
// `ocibnkctl init` when $OCIBNKCTL_BNK_FORGE_PATH or ~/git/bnk-forge
// exists locally; never blocks deployment when absent.
type BNKForge struct {
	Enabled       bool   `yaml:"enabled"`
	RepoPath      string `yaml:"repo_path,omitempty"`
	URL           string `yaml:"url,omitempty"`
	AdminUsername string `yaml:"admin_username,omitempty"`
	AdminPassword string `yaml:"admin_password,omitempty"`
	ProjectColor  string `yaml:"project_color,omitempty"`
	ProjectIcon   string `yaml:"project_icon,omitempty"`
}

type Status struct {
	Cluster     string    `yaml:"cluster"`
	Deploy      string    `yaml:"deploy"`
	LastPhaseAt time.Time `yaml:"last_phase_at,omitempty"`
}

// Phase tags used by status updates + e2e state.
const (
	PhaseCluster = "cluster"
	PhaseDeploy  = "deploy"
)

// TMMLabel returns the (key, value) the worker node is labelled with
// for TMM nodeSelector. Defaults match f5-bnk-udf convention.
func (b BNK) TMMLabel() (string, string) {
	k := b.TMMNodeLabelKey
	v := b.TMMNodeLabelValue
	if k == "" {
		k = "app"
	}
	if v == "" {
		v = "f5-tmm"
	}
	return k, v
}
