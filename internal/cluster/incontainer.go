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
