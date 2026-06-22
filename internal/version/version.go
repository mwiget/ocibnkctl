package version

// Build-time stamped values (see Makefile LDFLAGS).
var (
	Version    = "dev"
	Commit     = "none"
	BuildDate  = "unknown"
	BNKVersion = "2.3.0"
)

// Pinned defaults for BNK 2.3.0 running in demo-mode on the native k3s
// backend. The FLO, CIS, and cert-gen chart versions are NOT pinned
// here — they're resolved at deploy time from the f5-bigip-k8s-manifest
// release-manifest chart pulled from repo.f5.com (see
// internal/deploy/manifest.go), keyed off CNEManifestVersion below.
const (
	// K8sVersion is what we tell operators in docs/status.
	K8sVersion = "1.30"

	// K3sNodeImage is the rancher/k3s node-container image both the
	// server and agent run. Pinned to the v1.30.8 k8s minor BNK 2.3
	// declares supported; k3s names its image track "<k8s>-k3s1". It is
	// the default for poc.yaml's versions.node_image.
	K3sNodeImage = "rancher/k3s:v1.30.8-k3s1"

	// K8sToolsImage bundles kubectl + helm + openssl + apk so the CWC
	// cert-gen step (which shells gen_cert.sh inside this image) has
	// everything it needs. The container runtime is already required, so
	// the extra image pull at deploy time is cheap. Pinned one minor ahead
	// of K8sVersion so `kubectl wait --for=create` works against the
	// cluster (added in kubectl 1.31; supported back-skew is ±1 minor).
	K8sToolsImage = "alpine/k8s:1.31.5"

	// Cert-manager — required dependency for FLO + CWC. Same pin as
	// dpubnkctl (jetstack repo, not part of F5's release manifest).
	CertManagerChart   = "cert-manager"
	CertManagerRepo    = "https://charts.jetstack.io"
	CertManagerVersion = "v1.16.2"

	// Kyverno backs the optional `deploy shrink` step: a mutating
	// admission policy that caps CPU/memory *requests* on the F5 BNK
	// pods so the deployment schedules inside a much smaller host. The
	// FLO operator owns every workload spec via server-side-apply and
	// reasserts it on a tight reconcile loop, so the only layer that can
	// lower requests durably is admission — which runs AFTER FLO's apply.
	// Pinned; installed trimmed to the admission controller only.
	KyvernoChart   = "kyverno"
	KyvernoRepo    = "https://kyverno.github.io/kyverno/"
	KyvernoVersion = "3.8.1"

	// Release manifest — the F5 bill-of-materials chart that pins the
	// FLO + CIS + cert-gen + image versions for this BNK release. Pull
	// at deploy time; do NOT hardcode FLO chart version here.
	ReleaseManifestRepo  = "oci://repo.f5.com/release"
	ReleaseManifestChart = "f5-bigip-k8s-manifest"

	// CNEManifestVersion is the version coordinate inside the release
	// manifest. CNEInstance.spec.manifestVersion references it directly;
	// PullReleaseManifest uses it as helm pull --version arg.
	CNEManifestVersion = "2.3.0-3.2598.3-0.0.170"

	// FARRegistryHost is the OCI registry hostname for all F5-published
	// charts and images.
	FARRegistryHost = "repo.f5.com"

	// FLOChartOCIRef is the full OCI reference for the FLO chart. The
	// version is resolved at deploy time from the release-manifest chart.
	FLOChartOCIRef = "oci://repo.f5.com/charts/f5-lifecycle-operator"

	// CalicoManifestURL is the upstream Calico manifest applied right
	// after the k3s cluster is created (with its bundled CNI
	// disableDefaultCNI: true). Pinned to the same minor track BNK 2.3
	// declares as supported on the cluster side.
	CalicoManifestURL = "https://raw.githubusercontent.com/projectcalico/calico/v3.28.2/manifests/calico.yaml"

	// Whereabouts is the cluster-wide IPAM plugin for Multus secondary
	// interfaces. The base cluster ships none; the foundation layer installs
	// it so the bnk-bgp NAD's `ipam: {type: whereabouts}` hands each TMM
	// net1 a UNIQUE address from one shared cluster-wide pool — the
	// linchpin that lets a single NAD scale to N TMM workers (wholeCluster
	// DaemonSet) without per-node net1 IP collisions on the shared edge L2.
	// All three manifests are SHA-pinned (see deploy.EnsureWhereabouts); the
	// DaemonSet image is repinned off upstream's :latest to this tag.
	WhereaboutsTag               = "v0.8.0"
	WhereaboutsDaemonSetURL      = "https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/daemonset-install.yaml"
	WhereaboutsDaemonSetSHA      = "4292b79c85115823b697d99171eb701783eaecf13c0eb8d6a6657f1c5d86b2eb"
	WhereaboutsIPPoolCRDURL      = "https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/whereabouts.cni.cncf.io_ippools.yaml"
	WhereaboutsIPPoolCRDSHA      = "8957e53f260e5ee0ad6560743d02c609ff21e15f6c3b23851971522b3bf6027a"
	WhereaboutsReservationCRDURL = "https://raw.githubusercontent.com/k8snetworkplumbingwg/whereabouts/v0.8.0/doc/crds/whereabouts.cni.cncf.io_overlappingrangeipreservations.yaml"
	WhereaboutsReservationCRDSHA = "59ff31b1cbe8ff7732160f3f533a71884c4bb03b18d2aa48c97b0a2aadcc4cb0"

	// DockerNetworkInternal / External are the default names used when
	// poc.yaml leaves networks.{internal,external}.name unset. The k3s
	// cluster's two node containers both join these networks so
	// operator-supplied test client / router containers have a routable
	// path alongside TMM (which itself runs in demo mode and uses
	// virtio interfaces inside the pod netns, not these networks).
	DockerNetworkInternal = "bnk-internal"
	DockerNetworkExternal = "bnk-external"

	// Default subnets for the internal/external docker networks. RFC
	// 3849 / RFC 2544 style placeholders are intentional — these are
	// just "scenery" for client containers and are not reachable from
	// outside the laptop.
	DefaultInternalSubnet = "198.18.100.0/24"
	DefaultExternalSubnet = "203.0.113.0/24"
)

// ResourceSpec describes the operator-workstation floor a ocibnkctl
// deployment expects.
type ResourceSpec struct {
	Cores    int
	MemoryGB int
}

// MinBaseline + MinWithBNKForge are the floor the docker daemon (or
// Docker Desktop VM) must report so the BNK 2.3.0 chart actually
// schedules in the two-node demo shape. Kubernetes admits pods
// against `requests`, not RSS, and the F5 chart reserves heavily —
// but unlike kind, **k3s leaves the server node schedulable** (no
// control-plane NoSchedule taint), so the non-TMM pods spread across
// BOTH node containers rather than piling onto one worker. Each k3s
// node container reports the underlying daemon's full memory and CPU
// as its allocatable; k3s does not partition, so the scheduler packs
// each node up to the whole VM independently.
//
// Measured per-node `requests` on the fully loaded cluster — base BNK
// 2.3.0 + all 12 green how-to scenarios, 50 pods (measured 2026-06-06
// on a MacBook M4, Docker Desktop = 10 CPUs / ~15.6 Gi, stock CNE
// manifest 2.3.0-3.2598.3-0.0.170):
//
//   server (control-plane, schedulable)  9.41 cores · 13.3 Gi  → ~94% CPU
//   agent  (TMM, app=f5-tmm)             7.46 cores · 14.6 Gi  → ~94% mem
//   cluster total                        16.9 cores · 28.0 Gi  (split across 2 nodes)
//
// The binding constraint differs per node — the server peaks on CPU
// (9.4 of 10 cores), the agent on memory (TMM alone requests 9204 Mi) —
// and both land near 94% of a 10-core / 16 GB VM. That is exactly why
// the validated floor is 10 cores / 16 GB (the MacBook Air/Pro M4/M5
// shape) and why 9 cores would not schedule. Actual steady-state RSS is
// far smaller (~6.7 Gi / ~0.5 core total, even with every scenario up) —
// the floor is dictated by scheduling reservation, not by real usage.
//
// doctor enforces only Cores (runtime.NumCPU vs MinBaseline.Cores);
// MemoryGB here is documentation. MinWithBNKForge: bnk-forge runs as
// host-side containers OUTSIDE the Docker Desktop VM, so it needs no
// extra VM cores — just a little more host RAM.
var (
	MinBaseline     = ResourceSpec{Cores: 10, MemoryGB: 16}
	MinWithBNKForge = ResourceSpec{Cores: 10, MemoryGB: 18}

	// MinBaselineSmallHost is the floor for the small-host profile
	// (bnk.host_profile=small) — a Raspberry-Pi-class 4-core / 16 GB box.
	// It assumes BOTH the shrink policy (`deploy shrink`, which now also
	// caps kube-system) AND telemetry.metricSubsystem=false are in effect.
	// With metrics off, TMM drops to 3.4 cores (5 containers); the agent
	// node then carries TMM 3.4c + calico/multus (capped) ≈ 3.5c of 4.0c,
	// and the server node — every non-TMM pod capped to 25m — sits far
	// below. 4 cores is the floor because TMM alone is 3.4c and the
	// f5-tmm-pod-manager refuses to let it go lower (it enforces TMM's
	// per-container resource values; only removing the observer sidecar via
	// metricSubsystem=false is honored). Memory is dominated by TMM's blobd
	// (4 Gi) + main (2 Gi); ~16 GB leaves comfortable headroom.
	MinBaselineSmallHost = ResourceSpec{Cores: 4, MemoryGB: 16}
)

// BaselineFor returns the core/memory floor for a host profile. Unknown or
// empty profiles get the standard (10-core) floor.
func BaselineFor(profile string) ResourceSpec {
	if profile == "small" {
		return MinBaselineSmallHost
	}
	return MinBaseline
}

// Measured indicates whether the floor numbers above are real measured
// values or still placeholders.
func Measured() bool {
	return MinBaseline.Cores > 0 && MinBaseline.MemoryGB > 0
}

// PerExtraTMMNodeCores is the additional host CPU each TMM node beyond
// the first adds. Every k3s node runs as a container on the SAME host,
// so an extra TMM node does NOT add real capacity — it stacks another
// full-fat TMM (~7.46 cores measured for the agent node) onto the box.
// Rounded up to 8 so the floor errs on the side of engaging shrink.
const PerExtraTMMNodeCores = 8

// FloorForWorkers scales the standard core floor by the number of TMM
// nodes: one TMM fits MinBaseline; each extra TMM node piles another
// ~PerExtraTMMNodeCores onto the same host. Used by `e2e` to decide
// whether to auto-engage `deploy shrink` when scaling out on a tight box.
func FloorForWorkers(workers int) int {
	if workers < 1 {
		workers = 1
	}
	return MinBaseline.Cores + (workers-1)*PerExtraTMMNodeCores
}
