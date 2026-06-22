// Package bgppeer is the foundation scenario: it establishes BGP between every
// TMM's OcNOS and the EXTERNAL FRR container (the ToR) on the shared bnk-edge
// network. The other data-plane scenarios depend on it — they advertise Gateway
// VIPs that the external FRR learns, and issue their curls from inside the
// external FRR's netns (which holds those learned routes).
//
// This replaces the former in-cluster scn-frr pod: the external FRR is real,
// permanent cluster infrastructure (created by `cluster up` via
// cluster.EnsureEdge), so there is no per-scenario provisioning gate to skip —
// every data-plane scenario shares the one external FRR.
package bgppeer

import (
	"fmt"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

const (
	scnName  = "bgp-peer-frr"
	scnTitle = "BGP peering: TMM OcNOS ⇄ external FRR (ToR)"
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return nil }

func (s *scenario) Description() string {
	return "Establishes BGP between every TMM's OcNOS (AS 65000) and the external FRR " +
		"container (bnk-edge-frr-<cluster>, AS 65001) on the shared bnk-edge L2. The deploy " +
		"path already configures each TMM (anycast-bgp mode) to peer the external FRR; this " +
		"scenario (re)triggers BGP redistribution on every TMM so Gateway VIPs and kernel " +
		"routes are advertised, then verifies the external FRR sees each TMM Established. " +
		"Replaces the former in-cluster scn-frr pod — the external FRR is a real ToR/BGP peer, " +
		"and the data-plane scenarios curl from inside its netns."
}

// Manifests: none — the FRR peer is an external container created by the cluster
// backend, not an in-cluster manifest.
func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) { return nil, nil }

func (s *scenario) Apply(ctx *scenarios.Context) error {
	pods, err := tmmPods(ctx)
	if err != nil {
		return fmt.Errorf("list f5-tmm pods: %w", err)
	}
	if len(pods) == 0 {
		return fmt.Errorf("no Running f5-tmm pods — deploy first")
	}
	// Each TMM already peers the external FRR (configured by deploy in
	// anycast-bgp mode). imish auth (passwd.conf) + (re)trigger redistribution
	// on every TMM so its kernel/connected routes and any Gateway VIPs enter
	// the BGP table — OcNOS XP-6.6.0 only redistributes when the statement is
	// re-issued at runtime.
	for _, p := range pods {
		if err := injectPasswdConf(ctx, p); err != nil {
			return fmt.Errorf("inject passwd.conf on %s: %w", p, err)
		}
		triggerRedistribution(ctx, p)
	}
	fmt.Fprintf(ctx.Out, "      | triggered BGP redistribution on %d TMM(s); external FRR = %s\n",
		len(pods), scenarios.EdgeFRRName(ctx))
	return nil
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	res := scenarios.Result{}

	pods, _ := tmmPods(ctx)
	want := len(pods)
	if want == 0 {
		want = 1
	}

	// Poll the external FRR until every TMM neighbor is Established AND the
	// redistributed routes appear in FRR's BGP RIB (the session is functional,
	// not just up).
	var summary, rib string
	var got int
	var routesOK bool
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if out, err := scenarios.FRRVtysh(ctx, "show ip bgp summary"); err == nil {
			summary = out
			got = countEstablished(out, scenarios.EdgePrefix(ctx))
		}
		if out, err := scenarios.FRRVtysh(ctx, "show ip bgp"); err == nil {
			rib = out
			routesOK = strings.Contains(rib, "192.168.") || strings.Contains(rib, "203.0.113.")
		}
		if got >= want && routesOK {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Ctx.Done():
			res.Status = "failed"
			res.Summary = "context cancelled waiting for BGP"
			return res
		case <-time.After(5 * time.Second):
		}
	}

	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("external FRR (%s) shows all %d TMM neighbor(s) Established", scenarios.EdgeFRRIP(ctx), want),
		OK:          got >= want,
		Got:         fmt.Sprintf("%d/%d established; %s", got, want, oneLine(summary, 140)),
	})
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "external FRR learned routes from TMM (BGP RIB non-empty)",
		OK:          routesOK,
		Got:         oneLine(rib, 160),
	})

	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = fmt.Sprintf("%d TMM(s) Established with external FRR; routes learned", want)
	} else {
		res.Status = "failed"
		res.Summary = "BGP not fully established / no routes learned (see assertions)"
	}
	return res
}

// Cleanup: nothing to do — the external FRR is shared cluster infrastructure
// (owned by the cluster backend), not scenario-owned.
func (s *scenario) Cleanup(ctx *scenarios.Context) error { return nil }

// tmmPods returns the names of all Running f5-tmm pods (one per worker).
func tmmPods(ctx *scenarios.Context) ([]string, error) {
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-tmm", "--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// injectPasswdConf writes the one-line passwd.conf into a TMM pod's
// f5-tmm-routing container so imish auth works. Retries briefly.
func injectPasswdConf(ctx *scenarios.Context, pod string) error {
	r := ctx.Runner
	var err error
	for i := 0; i < 6; i++ {
		err = r.Kubectl(ctx.Ctx, "-n", "default", "exec", pod, "-c", "f5-tmm-routing", "--",
			"sh", "-c", "echo 'enable password 0 zebos' > /config/zebos/rd0/passwd.conf")
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Ctx.Done():
			return ctx.Ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return err
}

// triggerRedistribution re-issues the BGP redistribute statements over imish so
// OcNOS XP-6.6.0 (re-)runs its redistribution scan at runtime (it doesn't
// redistribute from the startup config). Best-effort + retried.
func triggerRedistribution(ctx *scenarios.Context, pod string) {
	r := ctx.Runner
	for i := 0; i < 3; i++ {
		_ = r.Kubectl(ctx.Ctx, "-n", "default", "exec", pod, "-c", "f5-tmm-routing", "--",
			"imish",
			"-e", "configure terminal",
			"-e", "router bgp 65000",
			"-e", "address-family ipv4 unicast",
			"-e", "redistribute kernel route-map RMALL",
			"-e", "redistribute connected route-map RMALL",
			"-e", "end")
		select {
		case <-ctx.Ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// countEstablished counts the bnk-edge (192.168.<octet>.x) BGP neighbor rows in
// FRR's `show ip bgp summary` that are Established (FRR shows an Up/Down timer
// like 00:02:13 in the State column; transient states show a name instead).
func countEstablished(summary, prefix string) int {
	n := 0
	for _, line := range strings.Split(summary, "\n") {
		if !strings.Contains(line, prefix) {
			continue
		}
		transient := false
		for _, t := range []string{"Idle", "Connect", "Active", "OpenSent", "OpenConfirm"} {
			if strings.Contains(line, t) {
				transient = true
				break
			}
		}
		if transient {
			continue
		}
		for _, f := range strings.Fields(line) {
			if strings.Count(f, ":") == 2 && !strings.HasPrefix(f, "192.") {
				n++
				break
			}
		}
	}
	return n
}

// oneLine collapses whitespace and truncates for compact assertion output.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
