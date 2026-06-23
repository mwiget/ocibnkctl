package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ClusterInfo summarizes one ocibnkctl k3s cluster found on the host.
type ClusterInfo struct {
	Name    string // the ocibnk.cluster label value
	Nodes   int    // node containers carrying the label (running + stopped)
	Running int    // node containers currently running
	Server  string // the control-plane/server container name
	APIPort string // host-mapped 6443 port of the server ("" if down/unknown)
}

// ListClusters enumerates the k3s clusters on the host by the `ocibnk.cluster`
// label every node container carries (see k3sClusterLabel). It works for both
// docker and podman via the shared `ps`/`port` CLI surface.
func ListClusters(ctx context.Context, rt Runtime) ([]ClusterInfo, error) {
	out, err := runtimeOut(ctx, rt, "ps", "-a",
		"--filter", "label="+k3sClusterLabel,
		"--format", "{{.Label \""+k3sClusterLabel+"\"}}\t{{.Names}}\t{{.State}}")
	if err != nil {
		return nil, err
	}
	byName := map[string]*ClusterInfo{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 2 {
			continue
		}
		name, cname := f[0], f[1]
		state := ""
		if len(f) == 3 {
			state = f[2]
		}
		ci := byName[name]
		if ci == nil {
			ci = &ClusterInfo{Name: name}
			byName[name] = ci
		}
		ci.Nodes++
		if strings.EqualFold(state, "running") {
			ci.Running++
		}
		if strings.HasSuffix(cname, "-server-0") {
			ci.Server = cname
		}
	}

	var infos []ClusterInfo
	for _, ci := range byName {
		if ci.Server == "" {
			ci.Server = "k3s-" + ci.Name + "-server-0"
		}
		// Best-effort API port (empty if the server is down).
		if port, err := serverAPIPort(ctx, rt, ci.Server); err == nil {
			ci.APIPort = port
		}
		infos = append(infos, *ci)
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

// ReadKubeconfig returns the named cluster's kubeconfig with its API endpoint
// rewritten to the live host-mapped `127.0.0.1:<port>` — the same transform
// WriteKubeconfig applies, but returning bytes so callers can install it
// wherever they like (e.g. ~/.kube/config). Errors if the server is absent.
func ReadKubeconfig(ctx context.Context, rt Runtime, name string) ([]byte, error) {
	server := "k3s-" + name + "-server-0"
	raw, err := runtimeOut(ctx, rt, "exec", server, "cat", "/etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig from %s (is the cluster running?): %w", server, err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty kubeconfig from %s", server)
	}
	port, err := serverAPIPort(ctx, rt, server)
	if err != nil {
		return nil, err
	}
	kc := strings.ReplaceAll(raw, "https://127.0.0.1:6443", "https://127.0.0.1:"+port)
	return []byte(kc), nil
}

// serverAPIPort returns the host port the server's 6443 is published on.
func serverAPIPort(ctx context.Context, rt Runtime, server string) (string, error) {
	out, err := runtimeOut(ctx, rt, "port", server, "6443")
	if err != nil {
		return "", fmt.Errorf("%s port %s 6443: %w", rt, server, err)
	}
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(out), "\n", 2)[0])
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return "", fmt.Errorf("cannot parse mapped API port from %q", line)
	}
	return line[idx+1:], nil
}

// runtimeOut runs `<rt> <args...>` and returns stdout, folding stderr noise
// into the error on failure.
func runtimeOut(ctx context.Context, rt Runtime, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, string(rt), args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w (%s)", rt, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}
