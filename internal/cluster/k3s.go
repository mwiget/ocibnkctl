package cluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// K3s provisions the two-node cluster by running rancher/k3s containers
// directly on the host's OCI runtime (docker or podman) — the same
// shape k3d wraps, but with no third-party orchestrator binary. One
// server container (combined control-plane + worker) and one agent
// container (the TMM worker, labelled app=f5-tmm by `cluster up`),
// joined over a per-cluster user-defined bridge network so the agent
// can resolve the server by container name via the runtime's embedded
// DNS. It implements Provisioner.
type K3s struct {
	Runtime Runtime
	Out     io.Writer
}

const (
	// k3sClusterLabel / k3sRoleLabel tag the node containers so
	// ClusterExists / DeleteCluster / NodeContainers can select them.
	k3sClusterLabel = "ocibnk.cluster"
	k3sRoleLabel    = "ocibnk.role"

	// k3sKubeconfigEnv points in-container kubectl at the server's
	// kubeconfig. The rancher/k3s image ships `kubectl` as its own
	// symlink (NOT `k3s kubectl`, which double-dispatches), so probes
	// run plain kubectl with this env set explicitly.
	k3sKubeconfigEnv = "KUBECONFIG=/etc/rancher/k3s/k3s.yaml"
)

// k3sServerArgs disable k3s's bundled flannel + network-policy so Calico
// is the CNI (nodes stay NotReady until `cluster up` applies the pinned
// Calico manifest), and drop the traefik/servicelb/metrics-server extras
// so the two-node footprint stays minimal. --tls-san=127.0.0.1 lets the
// extracted kubeconfig (rewritten to the host-mapped API port) verify.
var k3sServerArgs = []string{
	"--flannel-backend=none",
	"--disable-network-policy",
	"--disable=traefik",
	"--disable=servicelb",
	"--disable=metrics-server",
	"--tls-san=127.0.0.1",
}

func (k *K3s) rt() string {
	if k.Runtime == "" {
		return string(RuntimeDocker)
	}
	return string(k.Runtime)
}

// run builds an *exec.Cmd against the container runtime CLI.
func (k *K3s) run(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, k.rt(), args...)
}

// Backend reports this provisioner as k3s.
func (k *K3s) Backend() Backend { return BackendK3s }

// Tool reports the container runtime driving the nodes (docker/podman).
// There is no separate orchestrator binary to install.
func (k *K3s) Tool() string { return k.rt() }

// EnsurePresent verifies the container runtime is on PATH. No
// third-party cluster CLI (kind/k3d) is required.
func (k *K3s) EnsurePresent() error {
	if _, err := exec.LookPath(k.rt()); err != nil {
		return fmt.Errorf("%s not found on PATH — install a container runtime (docker or podman)", k.rt())
	}
	return nil
}

// ConfigArtifact is the filename the rendered plan is written to.
func (k *K3s) ConfigArtifact() string { return "k3s.yaml" }

// DefaultNodeImage is the pinned rancher/k3s image.
func (k *K3s) DefaultNodeImage() string { return version.K3sNodeImage }

func (k *K3s) network(name string) string    { return "k3s-" + name }
func (k *K3s) serverName(name string) string { return "k3s-" + name + "-server-0" }
func (k *K3s) agentName(name string) string  { return "k3s-" + name + "-agent-0" }

// WorkerNodeName is the agent's k8s node name (we pin --node-name to the
// container name, so node name == container name).
func (k *K3s) WorkerNodeName(name string) string { return k.agentName(name) }

// NodeContainerLabel selects this cluster's node containers.
func (k *K3s) NodeContainerLabel(name string) string {
	return k3sClusterLabel + "=" + name
}

// token is the shared server/agent join secret. These are throwaway
// local clusters (laptop demo shape), so a deterministic per-cluster
// token is acceptable and makes a re-created agent join idempotent.
func (k *K3s) token(name string) string { return "ocibnk-" + name }

// RenderConfig produces a documentary descriptor of the planned cluster
// for artifacts/ transparency. The native backend builds the cluster
// directly via `<runtime> run` rather than parsing this file, so it is
// purely informational — rendered from the same k3sServerArgs source so
// it never drifts from what is actually applied.
func (k *K3s) RenderConfig(name string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# ocibnkctl k3s backend — rendered plan (documentary; the cluster\n")
	fmt.Fprintf(&b, "# is built directly via `%s run`, not parsed from this file).\n", k.rt())
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "runtime: %s\n", k.rt())
	fmt.Fprintf(&b, "image: %s\n", k.DefaultNodeImage())
	fmt.Fprintf(&b, "network: %s\n", k.network(name))
	fmt.Fprintf(&b, "servers: 1\n")
	fmt.Fprintf(&b, "agents: 1\n")
	fmt.Fprintf(&b, "nodes:\n")
	fmt.Fprintf(&b, "  - name: %s   # control-plane + worker\n", k.serverName(name))
	fmt.Fprintf(&b, "  - name: %s    # TMM worker (app=f5-tmm)\n", k.agentName(name))
	fmt.Fprintf(&b, "serverArgs:\n")
	for _, a := range k3sServerArgs {
		fmt.Fprintf(&b, "  - %s\n", a)
	}
	return b.String(), nil
}

// ClusterExists reports whether the server container for this cluster is
// present (running or stopped).
func (k *K3s) ClusterExists(ctx context.Context, name string) (bool, error) {
	out, err := k.psQuiet(ctx, k3sClusterLabel+"="+name, k3sRoleLabel+"=server")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// psQuiet returns the container IDs matching all given label filters.
func (k *K3s) psQuiet(ctx context.Context, labels ...string) (string, error) {
	args := []string{"ps", "-a", "-q"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	c := k.run(ctx, args...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = io.Discard
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%s ps: %w", k.rt(), err)
	}
	return out.String(), nil
}

// CreateCluster brings up the server, waits for its API, then brings up
// the agent and waits for it to register. The config arg is ignored
// (the cluster is built from k3sServerArgs); callers gate on
// ClusterExists for idempotency.
func (k *K3s) CreateCluster(ctx context.Context, name, _, nodeImage string) error {
	if nodeImage == "" {
		nodeImage = k.DefaultNodeImage()
	}
	net := k.network(name)
	if err := k.ensureNetwork(ctx, net); err != nil {
		return err
	}

	server := k.serverName(name)
	serverArgs := []string{
		"run", "-d",
		"--name", server, "--hostname", server,
		"--privileged", "--restart=unless-stopped",
		"--network", net,
		"--label", k3sClusterLabel + "=" + name,
		"--label", k3sRoleLabel + "=server",
		"--tmpfs", "/run", "--tmpfs", "/var/run",
		"-e", "K3S_TOKEN=" + k.token(name),
		"-e", "K3S_KUBECONFIG_MODE=644",
		"-p", "127.0.0.1::6443",
		nodeImage, "server",
		"--node-name", server,
	}
	serverArgs = append(serverArgs, k3sServerArgs...)
	if err := k.runVisible(ctx, serverArgs...); err != nil {
		return fmt.Errorf("start k3s server: %w", err)
	}
	if err := k.makeRshared(ctx, server); err != nil {
		return fmt.Errorf("k3s server: %w", err)
	}
	if err := k.waitAPIReady(ctx, server, 3*time.Minute); err != nil {
		return fmt.Errorf("k3s server API not ready: %w", err)
	}

	agent := k.agentName(name)
	agentArgs := []string{
		"run", "-d",
		"--name", agent, "--hostname", agent,
		"--privileged", "--restart=unless-stopped",
		"--network", net,
		"--label", k3sClusterLabel + "=" + name,
		"--label", k3sRoleLabel + "=agent",
		"--tmpfs", "/run", "--tmpfs", "/var/run",
		"-e", "K3S_TOKEN=" + k.token(name),
		"-e", "K3S_URL=https://" + server + ":6443",
		nodeImage, "agent",
		"--node-name", agent,
	}
	if err := k.runVisible(ctx, agentArgs...); err != nil {
		return fmt.Errorf("start k3s agent: %w", err)
	}
	if err := k.makeRshared(ctx, agent); err != nil {
		return fmt.Errorf("k3s agent: %w", err)
	}
	if err := k.waitNodeCount(ctx, server, 2, 2*time.Minute); err != nil {
		return fmt.Errorf("agent node did not register: %w", err)
	}
	return nil
}

// makeRshared remounts the node container's rootfs as rshared so mount
// propagation works for in-pod mounts — Calico's mount-bpffs init
// container needs /sys to be a shared mount, but plain `docker run
// --privileged` mounts the rootfs rprivate. kind/k3d do the equivalent
// in their node entrypoints; the native backend does it explicitly.
// Retried briefly to absorb the gap between `run -d` returning and the
// container being exec-ready.
func (k *K3s) makeRshared(ctx context.Context, container string) error {
	var lastErr error
	for i := 0; i < 10; i++ {
		c := k.run(ctx, "exec", container, "mount", "--make-rshared", "/")
		var errb bytes.Buffer
		c.Stdout = io.Discard
		c.Stderr = &errb
		if err := c.Run(); err == nil {
			return nil
		} else if s := strings.TrimSpace(errb.String()); s != "" {
			lastErr = fmt.Errorf("%s", s)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("make rootfs rshared in %s: %w", container, lastErr)
}

// ensureNetwork creates the per-cluster user-defined bridge if absent.
func (k *K3s) ensureNetwork(ctx context.Context, net string) error {
	c := k.run(ctx, "network", "inspect", net)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if err := c.Run(); err == nil {
		return nil
	}
	return k.runVisible(ctx, "network", "create", net)
}

func (k *K3s) runVisible(ctx context.Context, args ...string) error {
	c := k.run(ctx, args...)
	c.Stdout = k.Out
	c.Stderr = k.Out
	return c.Run()
}

// waitAPIReady polls `k3s kubectl get --raw=/readyz` inside the server
// container until the API serves "ok" or the deadline passes.
func (k *K3s) waitAPIReady(ctx context.Context, server string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c := k.run(ctx, "exec", "-e", k3sKubeconfigEnv, server, "kubectl", "get", "--raw=/readyz")
		var out bytes.Buffer
		c.Stdout = &out
		c.Stderr = io.Discard
		if err := c.Run(); err == nil && strings.TrimSpace(out.String()) == "ok" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// waitNodeCount polls until at least want nodes are registered.
func (k *K3s) waitNodeCount(ctx context.Context, server string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		c := k.run(ctx, "exec", "-e", k3sKubeconfigEnv, server, "kubectl", "get", "nodes", "-o", "name")
		var out bytes.Buffer
		c.Stdout = &out
		c.Stderr = io.Discard
		if err := c.Run(); err == nil {
			if len(strings.Fields(out.String())) >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s waiting for %d nodes", timeout, want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// DeleteCluster removes the cluster's node containers and its network.
// Idempotent — a missing cluster is not an error.
func (k *K3s) DeleteCluster(ctx context.Context, name string) error {
	ids, err := k.psQuiet(ctx, k3sClusterLabel+"="+name)
	if err != nil {
		return err
	}
	if fields := strings.Fields(ids); len(fields) > 0 {
		if err := k.runVisible(ctx, append([]string{"rm", "-f"}, fields...)...); err != nil {
			return fmt.Errorf("remove k3s containers: %w", err)
		}
	} else {
		fmt.Fprintf(k.Out, "k3s cluster %q not present — nothing to delete\n", name)
	}
	// Network removal is best-effort (it may not exist, or may linger
	// briefly while containers detach).
	c := k.run(ctx, "network", "rm", k.network(name))
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	_ = c.Run()
	return nil
}

// WriteKubeconfig extracts the server's kubeconfig and rewrites its API
// endpoint to the host-mapped 127.0.0.1:<port>, writing it 0600.
func (k *K3s) WriteKubeconfig(ctx context.Context, name, path string) error {
	server := k.serverName(name)
	c := k.run(ctx, "exec", server, "cat", "/etc/rancher/k3s/k3s.yaml")
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return fmt.Errorf("read kubeconfig from %s: %w (%s)", server, err, strings.TrimSpace(errb.String()))
	}
	if strings.TrimSpace(out.String()) == "" {
		return fmt.Errorf("empty kubeconfig from %s", server)
	}
	port, err := k.apiHostPort(ctx, server)
	if err != nil {
		return err
	}
	kc := strings.ReplaceAll(out.String(), "https://127.0.0.1:6443", "https://127.0.0.1:"+port)
	return os.WriteFile(path, []byte(kc), 0o600)
}

// apiHostPort returns the host port the server's 6443 is published on.
func (k *K3s) apiHostPort(ctx context.Context, server string) (string, error) {
	c := k.run(ctx, "port", server, "6443")
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = io.Discard
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("%s port %s 6443: %w", k.rt(), server, err)
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out.String()), "\n", 2)[0])
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return "", fmt.Errorf("cannot parse mapped API port from %q", line)
	}
	return line[idx+1:], nil
}
