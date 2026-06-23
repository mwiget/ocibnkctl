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
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Versions   Versions `yaml:"versions"`
	Cluster    Cluster  `yaml:"cluster"`
	Networks   Networks `yaml:"networks"`
	BNK        BNK      `yaml:"bnk"`
	BNKForge   BNKForge `yaml:"bnk_forge,omitempty"`
	Status     Status   `yaml:"status"`
}

type Metadata struct {
	Name             string    `yaml:"name"`
	Customer         string    `yaml:"customer,omitempty"`
	Created          time.Time `yaml:"created"`
	OcibnkctlVersion string    `yaml:"ocibnkctl_version,omitempty"`
	BNKVersion       string    `yaml:"bnk_version"`
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

// Cluster is the cluster shape: one dedicated control node (server,
// tainted control-plane:NoSchedule) plus TMMNodes worker (agent) nodes,
// each labelled app=f5-tmm. Under wholeCluster, FLO runs TMM as a
// DaemonSet so one TMM pod lands on each labelled worker — scaling out is
// purely a matter of the worker count. The operator knobs are the cluster
// name, the container runtime (docker vs podman), and how many TMM nodes
// to run.
type Cluster struct {
	Name     string `yaml:"name"`
	Provider string `yaml:"provider"` // "docker" or "podman"
	// TMMNodes is the number of worker (agent) nodes dedicated to TMM,
	// each labelled app=f5-tmm. Defaults to 1 (use Workers()). Each TMM
	// node hosts one active TMM pod; tmmReplicas tracks this count, so N
	// nodes => N active TMMs (one per node). NOTE: transparent cross-node
	// throughput fan-out of a single VIP needs DPU/SR-IOV and is NOT
	// available in demo mode — each TMM serves the traffic that lands on
	// its own node.
	TMMNodes int `yaml:"tmm_nodes,omitempty"`
	// EdgeOctet is the 3rd octet of this PoC's bnk-edge network
	// (192.168.<EdgeOctet>.0/24) — the shared external L2 the workers
	// dual-home onto, carrying TMM net1 + the external FRR + origin.
	// Defaults to 99 (use EdgeNet()). Give each cluster a DISTINCT octet so
	// multiple clusters can run in parallel without docker subnet collisions.
	EdgeOctet int `yaml:"edge_octet,omitempty"`
	// RegistryCache opts this cluster into pulling images through a local
	// pull-through cache fleet (the companion `regcachectl` tool) so repeated
	// cluster create/destroy cycles stop re-pulling the same images. When
	// enabled, `cluster up` renders a k3s registries.yaml mapping each upstream
	// (docker.io/ghcr.io/quay.io/repo.f5.com) to http://<Host>:<PortBase+i>
	// with the real upstream as a fallback, and bind-mounts it into every node.
	// Opt-in: unset → today's direct-pull behaviour, no change.
	RegistryCache RegistryCache `yaml:"registry_cache,omitempty"`
	// TEEMSRelay opts this cluster into the host-side TEEMS egress relay — a
	// workaround for hosts where FORWARDED pod egress is lossy but host-
	// originated egress is fine (e.g. a Hetzner box that drops a fraction of
	// NAT'd/forwarded TCP). The CWC's license POST to F5's TEEMS backend is the
	// only thing that breaks under that loss, and the CWC ignores HTTPS_PROXY,
	// so `cluster up` runs a host-netns socat relay that re-originates the TEEMS
	// connection from the host stack + DNATs the cluster's forwarded TEEMS
	// traffic onto it. `destroy` removes it. Opt-in: unset → egress unchanged.
	TEEMSRelay bool `yaml:"teems_relay,omitempty"`
}

// RegistryCache configures the optional local pull-through cache wiring.
// The cache fleet itself is managed out-of-band by `regcachectl up`; this
// block only steers the cluster's nodes at it.
type RegistryCache struct {
	Enabled bool `yaml:"enabled"`
	// Host is the address the k3s node containers use to reach the
	// host-published caches. Defaults to host.docker.internal (resolved via
	// an --add-host host-gateway alias added to every node). Override with a
	// bridge-gateway IP if host.docker.internal is unavailable.
	Host string `yaml:"host,omitempty"`
	// PortBase is the host port of the first cache (docker.io); the rest
	// follow in order. Defaults to 5000, matching regcachectl's layout.
	PortBase int `yaml:"port_base,omitempty"`
}

// CacheHost returns the cache host, defaulting to host.docker.internal.
func (r RegistryCache) CacheHost() string {
	if r.Host == "" {
		return "host.docker.internal"
	}
	return r.Host
}

// CachePortBase returns the first cache's host port, defaulting to 5000.
func (r RegistryCache) CachePortBase() int {
	if r.PortBase <= 0 {
		return 5000
	}
	return r.PortBase
}

// Workers returns the configured TMM/agent node count, defaulting to 1
// when unset (TMMNodes == 0) or invalid.
func (c Cluster) Workers() int {
	if c.TMMNodes < 1 {
		return 1
	}
	return c.TMMNodes
}

// EdgeNet returns the bnk-edge 3rd octet, defaulting to 99 when unset or out
// of range. The edge network is 192.168.<EdgeNet()>.0/24.
func (c Cluster) EdgeNet() int {
	if c.EdgeOctet < 1 || c.EdgeOctet > 254 {
		return 99
	}
	return c.EdgeOctet
}

// MaxTMMNodes caps tmm_nodes. It's a guard-rail, not a hard product
// limit; BNK scales TMM to 32 pods, and under wholeCluster each labelled
// worker carries one TMM DaemonSet pod, so 32 matches the product ceiling.
// The demo shape runs every k3s node as a container on one host, so this
// still keeps an accidental large value from oversubscribing the box.
const MaxTMMNodes = 32

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
	// ActiveActive is the legacy bool for the all-active multi-TMM data
	// plane. DEPRECATED in favour of TMMDataplaneMode — kept as a
	// back-compat alias: `tmm_active_active: true` is equivalent to
	// `tmm_dataplane_mode: selfip-dag`. Use DataplaneMode() rather than
	// reading this field directly; it folds the legacy bool into the
	// three-value enum and is the single source of truth for the deploy
	// path.
	//
	// When set, `deploy` installs Multus, attaches a bridge NAD to every
	// TMM (mapres grabs net1 as interface 1.1), and applies an F5SPKVlan
	// with one self-IP per TMM node plus a pod_hash stateless DAG — so
	// each TMM owns a self-IP and is active rather than standby.
	//
	// Each TMM still serves only the traffic that lands on its own node;
	// transparent cross-node fan-out of one VIP's throughput needs
	// DPU/SR-IOV and is not available in the demo shape.
	ActiveActive bool `yaml:"tmm_active_active,omitempty"`
	// TMMDataplaneMode selects how multi-node TMM presents its data
	// plane. The two all-active modes are mutually exclusive on net1
	// (one needs mapres TRUE, the other FALSE), so a single bool can't
	// express all three states — hence a string enum:
	//
	//   "" / "standby"  — default. No NAD on net1; mapres TRUE. One TMM
	//                     active, the rest standby (BNK's stock HA shape).
	//   "selfip-dag"    — today's all-active path: bridge NAD on net1,
	//                     mapres TRUE (interface 1.1), F5SPKVlan with one
	//                     self-IP per TMM + pod_hash DAG. The ONLY mode
	//                     that needs no upstream router. Legacy
	//                     `tmm_active_active: true` aliases to this.
	//   "anycast-bgp"   — new: every per-node TMM runs mapres FALSE and
	//                     advertises the same VIP /32 over its own
	//                     ZeBOS/BGP session, so an upstream router (ToR/
	//                     FRR) ECMP-load-balances across the TMM pods
	//                     (anycast). Builds on the bgp-peer-frr scenario.
	//
	// Use DataplaneMode()/IsSelfIPDAG()/IsAnycastBGP() rather than this
	// field directly — they fold in the legacy ActiveActive bool.
	TMMDataplaneMode string `yaml:"tmm_dataplane_mode,omitempty"`
}

// Host profile values for BNK.HostProfile.
const (
	HostProfileStandard = "standard"
	HostProfileSmall    = "small"
)

// TMM data-plane mode values for BNK.TMMDataplaneMode.
const (
	DataplaneStandby    = "standby"
	DataplaneSelfIPDAG  = "selfip-dag"
	DataplaneAnycastBGP = "anycast-bgp"
)

// DataplaneMode returns the effective TMM data-plane mode, folding the
// legacy ActiveActive bool into the three-value enum. Precedence: an
// explicit TMMDataplaneMode wins; otherwise `tmm_active_active: true`
// maps to selfip-dag; otherwise standby. Validation (see Validate)
// rejects a poc.yaml that sets both fields to disagreeing values, so by
// the time the deploy path calls this the two are consistent.
func (b BNK) DataplaneMode() string {
	if b.TMMDataplaneMode != "" {
		return b.TMMDataplaneMode
	}
	if b.ActiveActive {
		return DataplaneSelfIPDAG
	}
	// Default in the wholeCluster/DaemonSet architecture: anycast-bgp. Every
	// per-worker TMM gets a net1 (whereabouts IPAM) on the bnk-bgp NAD and
	// advertises its VIP /32 over its own BGP session — the natural scale-out
	// as the TMM worker count grows. It also guarantees a non-empty
	// networkAttachments, which wholeCluster needs (empty + wholeCluster
	// tripped a FLO nil-map panic on BNK 2.3). Set tmm_dataplane_mode:
	// standby explicitly to opt out (single-TMM, no NAD).
	return DataplaneAnycastBGP
}

// IsSelfIPDAG reports whether the effective data-plane mode is the
// self-IP + DAG all-active path (the original ActiveActive shape).
func (b BNK) IsSelfIPDAG() bool { return b.DataplaneMode() == DataplaneSelfIPDAG }

// IsAnycastBGP reports whether the effective data-plane mode is the
// BGP-anycast all-active path (mapres FALSE, VIP /32 over ZeBOS/BGP).
func (b BNK) IsAnycastBGP() bool { return b.DataplaneMode() == DataplaneAnycastBGP }

// IsAllActive reports whether either all-active data-plane mode is in
// effect (selfip-dag or anycast-bgp) — i.e. not plain standby.
func (b BNK) IsAllActive() bool { return b.DataplaneMode() != DataplaneStandby }

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
