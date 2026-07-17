// Package runtimeenv answers "where is ocibnkctl running?" — host vs container.
// It is a leaf package (stdlib only) so both cluster and deploy can depend on
// it without an import cycle. It matters because ocibnkctl is a host tool by
// design: as a BNK Forge container-runner artifact, host assumptions break
// (loopback addresses, and `docker run -v <local-path>` binds a path the host
// daemon cannot see). Callers branch on InContainer() to stay correct in both.
package runtimeenv

import (
	"fmt"
	"os"
	"strings"
)

// InContainer reports whether ocibnkctl is running inside a container.
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

// SelfContainerID returns the running container's own ID. Docker sets the
// container hostname to the short container ID by default, a valid reference
// for `docker network connect`. A caller-supplied hostname override would break
// this; callers treat failures as non-fatal.
func SelfContainerID() (string, error) {
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
