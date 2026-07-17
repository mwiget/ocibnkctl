package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/runtimeenv"
)

// InContainer is re-exported from runtimeenv for existing cluster-package
// callers (WriteKubeconfig, the registration gate).
func InContainer() bool { return runtimeenv.InContainer() }

// ConnectSelfToNetwork attaches ocibnkctl's own container to net so kubectl,
// running here, can reach the k3s node containers by their network IP. Docker
// bridge isolation otherwise blocks a container on one network from routing to
// another. Idempotent: an "already exists" error is treated as success.
func (d *DockerCLI) ConnectSelfToNetwork(ctx context.Context, net string) error {
	self, err := runtimeenv.SelfContainerID()
	if err != nil {
		return err
	}
	c := d.cmd(ctx, "network", "connect", net, self)
	raw, err := c.CombinedOutput()
	out := string(raw)
	if err != nil {
		if strings.Contains(out, "already exists") || strings.Contains(out, "already attached") {
			return nil
		}
		return fmt.Errorf("network connect %s %s: %w (%s)", net, self, err, strings.TrimSpace(out))
	}
	return nil
}

// EnsureReachable makes the k3s apiserver reachable from wherever ocibnkctl
// runs. On the host it is a no-op (the kubeconfig's host-loopback works). In a
// container it attaches this container to the cluster's docker network, so the
// kubeconfig's server-container-IP endpoint routes. Idempotent and safe to call
// as a preflight before every cluster-touching subcommand — crucial on an e2e
// RESUME, where cluster-up (which also attaches, via WriteKubeconfig) is skipped
// but the deploy phases still need connectivity.
func EnsureReachable(ctx context.Context, provider, clusterName string) error {
	if !runtimeenv.InContainer() {
		return nil
	}
	dc := &DockerCLI{Runtime: Runtime(provider)}
	return dc.ConnectSelfToNetwork(ctx, "k3s-"+clusterName)
}
