package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// InContainer reports whether ocibnkctl is running INSIDE a container (e.g. as
// a BNK Forge container-runner artifact) rather than on the host.
//
// It matters because the host-published apiserver port in the kubeconfig
// (127.0.0.1:<mapped>) is unreachable from inside a container — 127.0.0.1 is
// the container's own loopback — and the k3s node containers live on their own
// docker network that a container on a different network cannot route to.
// Detected via /.dockerenv (Docker) and the cgroup path (podman/containerd),
// overridable with OCIBNKCTL_IN_CONTAINER for runners that hide both.
func InContainer() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCIBNKCTL_IN_CONTAINER"))) {
	case "1", "true", "yes":
		return true
	case "0", "false", "no":
		return false
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		body := string(data)
		if strings.Contains(body, "docker") || strings.Contains(body, "containerd") ||
			strings.Contains(body, "libpod") || strings.Contains(body, "kubepods") {
			return true
		}
	}
	return false
}

// selfContainerID returns the running container's own ID. Docker sets the
// container hostname to the short container ID by default, which is a valid
// reference for `docker network connect`. A caller-supplied hostname override
// (rare) would break this; that is why network-attach failures are non-fatal.
func selfContainerID() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "", err
	}
	h = strings.TrimSpace(h)
	if h == "" {
		return "", fmt.Errorf("empty hostname; cannot determine own container id")
	}
	return h, nil
}

// ConnectSelfToNetwork attaches ocibnkctl's own container to net so kubectl,
// running here, can reach the k3s node containers by their network IP. Docker
// bridge isolation otherwise blocks a container on one network from routing to
// another. Idempotent: an "already exists" error is treated as success.
func (d *DockerCLI) ConnectSelfToNetwork(ctx context.Context, net string) error {
	self, err := selfContainerID()
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
