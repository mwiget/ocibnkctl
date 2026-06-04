package cluster

import (
	"context"
	"fmt"
	"io"
)

// Backend identifies the local-Kubernetes provisioner. ocibnkctl ships
// a single backend: k3s nodes run directly as containers on the host's
// OCI runtime (docker or podman), driven through the runtime CLI with
// no third-party orchestrator binary (no kind, no k3d). The Provisioner
// interface is retained as a seam so an alternative backend could be
// added without disturbing the CLI's cluster-up / destroy paths.
type Backend string

const (
	BackendK3s Backend = "k3s"
)

// Provisioner is the backend-agnostic surface the CLI's `cluster up`
// and `destroy` paths drive. K3s satisfies it. The methods abstract the
// few operator-visible specifics: the config-file shape written to
// artifacts/ (RenderConfig / ConfigArtifact), the k8s node name the TMM
// worker ends up with (WorkerNodeName), and the container label the
// node containers carry (NodeContainerLabel).
type Provisioner interface {
	// Backend reports which provisioner this is.
	Backend() Backend
	// Tool is the container runtime CLI driving the nodes ("docker" /
	// "podman"). There is no separate orchestrator binary.
	Tool() string
	// EnsurePresent verifies the container runtime is installed.
	EnsurePresent() error
	// RenderConfig produces the backend's cluster-config file body for
	// a two-node (1 control-plane/server + 1 worker/agent) cluster with
	// the default CNI disabled so Calico can be layered on top.
	RenderConfig(name string) (string, error)
	// ConfigArtifact is the filename the rendered config is written to
	// under artifacts/ (k3s.yaml).
	ConfigArtifact() string
	// ClusterExists reports whether a cluster of this name is present.
	ClusterExists(ctx context.Context, name string) (bool, error)
	// CreateCluster brings the cluster up from the rendered config.
	// nodeImage overrides the per-backend default node image when set.
	CreateCluster(ctx context.Context, name, config, nodeImage string) error
	// DeleteCluster tears the cluster down (idempotent).
	DeleteCluster(ctx context.Context, name string) error
	// WriteKubeconfig writes the cluster kubeconfig to path (mode 0600).
	WriteKubeconfig(ctx context.Context, name, path string) error
	// WorkerNodeName is the k8s node name of the non-control-plane node
	// (where TMM is pinned via the app=f5-tmm label).
	WorkerNodeName(name string) string
	// NodeContainerLabel is the `docker ps --filter label=…` selector
	// that matches this cluster's node containers.
	NodeContainerLabel(name string) string
	// DefaultNodeImage is the backend's pinned node image used when the
	// caller doesn't override it.
	DefaultNodeImage() string
}

// NewProvisioner returns the Provisioner for the chosen backend, wired
// to the given container runtime and progress writer.
func NewProvisioner(b Backend, rt Runtime, out io.Writer) (Provisioner, error) {
	switch b {
	case BackendK3s:
		return &K3s{Runtime: rt, Out: out}, nil
	default:
		return nil, fmt.Errorf("unknown cluster backend %q (want k3s)", b)
	}
}
