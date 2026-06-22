package scenarios

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/cluster"
)

// External edge fabric — the BGP peer (FRR) and upstream (origin) run as their
// own docker containers on the shared bnk-edge network (created by `cluster up`
// via cluster.EnsureEdge), not as in-cluster pods. These helpers let scenarios
// target them: BGP checks via `vtysh` in the FRR container, and data-plane
// curls from INSIDE the FRR container's network namespace (so they route via
// FRR's BGP-learned routes — the way to reach Gateway VIPs that are only
// reachable through TMM). This replaces the old per-scenario scn-frr pod: the
// external FRR is permanent cluster infrastructure shared by every scenario.
const (
	EdgeOriginDest = cluster.EdgeOriginDest // off-subnet /32 alias, reachable only via TMM
	EdgeOriginMark = cluster.EdgeOriginMark // body the origin returns on every dest

	// edgeToolsImage is the throwaway image joined to the FRR netns for curls.
	edgeToolsImage = "nicolaka/netshoot"
)

// EdgeFRRIP / EdgeOriginIP are octet-parameterized per PoC (cluster.edge_octet,
// default 99) so parallel clusters don't collide — derived from the Context's PoC.
func EdgeFRRIP(ctx *Context) string    { return cluster.EdgeFRRIP(ctx.PoC.Cluster.EdgeNet()) }    // 192.168.<octet>.41, AS 65001
func EdgeOriginIP(ctx *Context) string { return cluster.EdgeOriginIP(ctx.PoC.Cluster.EdgeNet()) } // 192.168.<octet>.50

// EdgePrefix is "192.168.<octet>." — the bnk-edge address prefix for substring checks.
func EdgePrefix(ctx *Context) string { return fmt.Sprintf("192.168.%d.", ctx.PoC.Cluster.EdgeNet()) }

// EdgeFRRName / EdgeOriginName are the external container names for this PoC.
func EdgeFRRName(ctx *Context) string    { return "bnk-edge-frr-" + ctx.PoC.Cluster.Name }
func EdgeOriginName(ctx *Context) string { return "bnk-edge-origin-" + ctx.PoC.Cluster.Name }

// runtimeBin is the container runtime (docker/podman) from poc.yaml.
func runtimeBin(ctx *Context) string {
	if p := ctx.PoC.Cluster.Provider; p != "" {
		return p
	}
	return "docker"
}

// runtimeCapture runs `<runtime> <args...>` and returns combined output.
func runtimeCapture(ctx *Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx.Ctx, runtimeBin(ctx), args...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	return out.String(), err
}

// FRRExec runs a command inside the external FRR container.
func FRRExec(ctx *Context, args ...string) (string, error) {
	return runtimeCapture(ctx, append([]string{"exec", EdgeFRRName(ctx)}, args...)...)
}

// FRRVtysh runs a single vtysh command in the external FRR container.
func FRRVtysh(ctx *Context, cmd string) (string, error) {
	return FRRExec(ctx, "vtysh", "-c", cmd)
}

// OriginExec runs a command inside the external origin container.
func OriginExec(ctx *Context, args ...string) (string, error) {
	return runtimeCapture(ctx, append([]string{"exec", EdgeOriginName(ctx)}, args...)...)
}

// FRRNetnsCurl issues a curl from inside the external FRR's network namespace
// (an ephemeral netshoot joined to the FRR netns), so it routes via FRR's
// BGP-learned routes — used to reach Gateway VIPs that are only reachable
// through TMM. Returns the response body; errors carry stderr.
func FRRNetnsCurl(ctx *Context, url string, extraCurlArgs ...string) (string, error) {
	args := []string{"run", "--rm", "--network", "container:" + EdgeFRRName(ctx),
		edgeToolsImage, "curl", "-sS", "--fail", "--max-time", "8"}
	args = append(args, extraCurlArgs...)
	args = append(args, url)
	c := exec.CommandContext(ctx.Ctx, runtimeBin(ctx), args...)
	var out, errb bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errb
	if err := c.Run(); err != nil {
		return out.String(), fmt.Errorf("curl %s from FRR netns: %w (%s)",
			url, err, strings.TrimSpace(oneLineEdge(errb.String(), 200)))
	}
	return out.String(), nil
}

// FRRNetnsRun runs an arbitrary command in an ephemeral netshoot joined to the
// external FRR's network namespace — the general form of FRRNetnsCurl, for
// data-plane tools other than curl (socat for UDP, a bind-mounted grpcurl,
// etc.). extraRunArgs are inserted before the image (e.g. "-v host:ctr:ro"
// mounts). Returns combined stdout+stderr so callers can inspect tool output.
func FRRNetnsRun(ctx *Context, extraRunArgs []string, cmd ...string) (string, error) {
	args := []string{"run", "--rm", "--network", "container:" + EdgeFRRName(ctx)}
	args = append(args, extraRunArgs...)
	args = append(args, edgeToolsImage)
	args = append(args, cmd...)
	return runtimeCapture(ctx, args...)
}

// RetriggerRedistribute re-issues the OcNOS BGP `redistribute kernel/connected
// route-map RMALL` statements on every TMM pod's f5-tmm-routing daemon. OcNOS
// XP-6.6.0 only injects a redistributed route into BGP when the statement is
// (re-)issued at RUNTIME after the route exists — so a Gateway VIP /32 created
// by a data-plane scenario's Apply (a kernel route on TMM) is NOT advertised to
// the external FRR until this runs. The deploy + bgp-peer-frr only trigger once,
// before any data-plane VIP exists, so every FRR-vantage scenario must re-trigger
// itself. Best-effort + noisy (imish prints stray errors); the caller polls the
// FRR BGP table to confirm the VIP actually converged, so call this INSIDE that
// poll loop. AS 65000 matches BGPTMMAS / the bgp-peer-frr scenario.
func RetriggerRedistribute(ctx *Context) {
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pods",
		"-l", "app=f5-tmm", "-o",
		`jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return
	}
	for _, pod := range strings.Fields(out) {
		if pod == "" {
			continue
		}
		// imish -f a tiny config file: unlike repeated `-e` flags (each of which
		// runs in exec mode and never enters router-bgp config context), a -f
		// script keeps config-mode context across lines, so the redistribute is
		// actually re-issued and OcNOS rescans the kernel RIB.
		_ = ctx.Runner.Kubectl(ctx.Ctx, "-n", "default", "exec", pod,
			"-c", "f5-tmm-routing", "--", "sh", "-c",
			`printf 'configure terminal\nrouter bgp 65000\naddress-family ipv4 unicast\nredistribute kernel route-map RMALL\nredistribute connected route-map RMALL\nexit\nexit\nexit\n' > /tmp/ocibnkctl-redist.cfg; imish -f /tmp/ocibnkctl-redist.cfg`)
	}
}

func oneLineEdge(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
