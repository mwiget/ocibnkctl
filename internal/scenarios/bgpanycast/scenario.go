// Package bgpanycast implements scenario "bgp-anycast": a verification-only
// scenario for the anycast-bgp data-plane mode (the default). It applies
// nothing — the deploy path already stood up the bnk-bgp NAD, flipped mapres
// FALSE, installed the cluster-wide OcNOS template, and the cluster backend
// runs the EXTERNAL bnk-edge FRR every TMM peers. The scenario asserts the
// anycast model against that one external FRR (the shared ToR).
//
// The edge fabric is the key win over the old per-node-isolated bridges: every
// TMM net1 lands on ONE shared L2 with ONE external FRR, so the FRR really does
// see all N TMMs as ECMP next-hops for the advertised routes — genuine
// cross-worker anycast fan-out, not just the per-node model.
package bgpanycast

import (
	"fmt"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

const (
	scnName  = "bgp-anycast"
	scnTitle = "BGP anycast all-active data plane (how-to #3) — every TMM advertises over the shared edge FRR"
	tmmLabel = "app=f5-tmm"
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{"bgp-peer-frr"} }

func (s *scenario) Description() string {
	return strings.TrimSpace(`
Verification-only scenario for the anycast-bgp data-plane mode (the default).
It applies nothing — the deploy path installed the bnk-bgp NAD, mapres=FALSE,
and the cluster-wide OcNOS template, and the cluster backend runs the external
bnk-edge FRR (the ToR) every TMM peers. This scenario asserts the anycast model
against that one external FRR:

  - the external FRR is reachable (else the cluster isn't in the edge-fabric
    anycast shape — skips cleanly)
  - every Running TMM has its own BGP session Established to the external FRR
    (count of Established == TMM-pod count)
  - the external FRR learned the TMM-advertised routes (BGP RIB non-empty)
  - the FRR has MULTIPLE next-hops for the TMM net1 subnet — real ECMP fan-out
    across workers, now that all TMMs share one L2 with one FRR (the edge-fabric
    improvement over per-node-isolated bridges)
`)
}

// Manifests / Apply / Cleanup: nothing — every resource this scenario inspects
// is owned by the deploy path + the cluster backend's external FRR.
func (s *scenario) Manifests(*scenarios.Context) ([]string, error) { return nil, nil }
func (s *scenario) Apply(*scenarios.Context) error                 { return nil }
func (s *scenario) Cleanup(*scenarios.Context) error               { return nil }

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	r := ctx.Runner
	res := scenarios.Result{}
	prefix := scenarios.EdgePrefix(ctx)

	// Precondition: the external bnk-edge FRR must be reachable. Absent →
	// skip cleanly (the cluster isn't in the edge-fabric anycast shape).
	if _, err := scenarios.FRRVtysh(ctx, "show version"); err != nil {
		res.Status = "skipped"
		res.Summary = "external bnk-edge FRR not reachable — cluster not in anycast-bgp edge-fabric mode"
		return res
	}

	// One BGP neighbor per Running TMM (anycast: every TMM peers the one FRR).
	tmmOut, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod", "-l", tmmLabel,
		"--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	want := len(strings.Fields(tmmOut))
	if want == 0 {
		want = 1
	}

	// Poll the external FRR until every TMM neighbor is Established and routes
	// are learned.
	var summary, rib string
	var established int
	var routesOK bool
	deadline := time.Now().Add(4 * time.Minute)
	for {
		if out, err := scenarios.FRRVtysh(ctx, "show ip bgp summary"); err == nil {
			summary = out
			established = countEstablished(out, prefix)
		}
		if out, err := scenarios.FRRVtysh(ctx, "show ip bgp"); err == nil {
			rib = out
			routesOK = strings.Contains(rib, prefix) || strings.Contains(rib, "203.0.113.")
		}
		if established >= want && routesOK {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Ctx.Done():
			res.Status = "failed"
			res.Summary = "context cancelled waiting for anycast BGP"
			return res
		case <-time.After(5 * time.Second):
		}
	}

	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("external FRR (%s) sees all %d TMM(s) Established (one anycast next-hop per worker)", scenarios.EdgeFRRIP(ctx), want),
		OK:          established >= want,
		Got:         fmt.Sprintf("%d/%d established; %s", established, want, oneLine(summary, 140)),
	})
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "external FRR learned routes from the TMM anycast group (BGP RIB non-empty)",
		OK:          routesOK,
		Got:         oneLine(rib, 200),
	})

	// The anycast win: the FRR should have MULTIPLE BGP paths for the TMM net1
	// subnet (one per worker) — real ECMP fan-out across workers. Best-effort:
	// `show bgp ipv4 unicast <cidr>` prints one line per path; count the edge
	// next-hop occurrences. Single-worker clusters trivially pass.
	net1CIDR := prefix + "0/24"
	mp, _ := scenarios.FRRVtysh(ctx, "show bgp ipv4 unicast "+net1CIDR)
	paths := strings.Count(mp, prefix)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("external FRR has %d+ BGP path(s) for the TMM net1 subnet (ECMP fan-out across workers)", want),
		OK:          want < 2 || paths >= 2,
		Got:         fmt.Sprintf("~%d path next-hop(s) for %s", paths, net1CIDR),
	})

	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = fmt.Sprintf("%d TMM(s) anycast-Established with the external FRR; routes learned + ECMP fan-out", want)
	} else {
		res.Status = "failed"
		var failed []string
		for _, a := range res.Assertions {
			if !a.OK {
				failed = append(failed, a.Description)
			}
		}
		res.Summary = "failed: " + strings.Join(failed, "; ")
	}
	return res
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
			if strings.Count(f, ":") == 2 && !strings.HasPrefix(f, prefix) {
				n++
				break
			}
		}
	}
	return n
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
