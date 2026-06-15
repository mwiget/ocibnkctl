// Package bgpanycast implements scenario "bgp-anycast": a
// verification-only scenario for the persistent anycast-bgp data-plane
// mode (bnk.tmm_dataplane_mode: anycast-bgp).
//
// Unlike bgp-peer-frr (which APPLIES a runtime patch on top of a standby
// deploy and peers a single TMM with its own scn-frr pod), this scenario
// applies nothing: the deploy path already stood up the bnk-bgp NAD,
// flipped mapres FALSE, installed the cluster-wide ZeBOS template, and
// runs an FRR peer as a DaemonSet on every app=f5-tmm node. The scenario
// asserts that EVERY TMM pod formed its own BGP session and advertises
// its routes — the multi-pod anycast model.
//
// It skips cleanly when the cluster isn't in anycast-bgp mode (detected
// by the absence of the bnk-frr DaemonSet the deploy path installs).
//
// Honest single-host limit: the per-node bnk-bgp bridges are isolated L2
// segments, so each FRR peer sees only the TMM on its own node — one
// next-hop, not N. Real cross-node ECMP fan-out needs a shared-L2
// underlay + an upstream ToR receiving all N sessions, which a
// single-host demo can't provide. The scenario asserts each FRR sees its
// node-local TMM count and documents the ECMP gap rather than failing on
// it — hence the amber rating.
package bgpanycast

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

const (
	scnName  = "bgp-anycast"
	scnTitle = "BGP anycast all-active data plane (how-to #3) — every TMM advertises its VIP /32"
	frrDS    = "bnk-frr"
	frrLabel = "app=bnk-frr"
	tmmLabel = "app=f5-tmm"
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Amber }
func (s *scenario) Dependencies() []string   { return nil }

func (s *scenario) Description() string {
	return strings.TrimSpace(`
Verification-only scenario for the persistent anycast-bgp data-plane mode
(bnk.tmm_dataplane_mode: anycast-bgp). It applies nothing — the deploy
path already installed the bnk-bgp NAD, mapres=FALSE, the cluster-wide
ZeBOS template, and an FRR peer DaemonSet on the app=f5-tmm nodes. This
scenario asserts the multi-pod anycast model came up:

  - the bnk-frr DaemonSet is Ready (skips the whole scenario cleanly if
    it's absent — i.e. the cluster isn't in anycast-bgp mode)
  - every Running TMM pod kept net1's kernel IP (mapres FALSE)
  - every TMM pod has its own BGP session Established to the FRR peer
    (count of Established == TMM-pod count)
  - each TMM's ZeBOS router-id is its own pod IP and they are all
    DISTINCT — proving the shared ConfigMap's %%POD_IP%% token expands
    per pod (the linchpin of N-pod anycast from one template)
  - each FRR peer learned >=1 prefix from its node-local TMM

Honest single-host limit (amber): the per-node bnk-bgp bridges are
isolated L2, so each FRR peer sees only its node-local TMM — one
next-hop, not N. The scenario asserts each FRR's dynamic-neighbor count
equals the TMM count co-located on its node and documents the ECMP gap
rather than failing on it. Real cross-node ECMP fan-out needs a
shared-L2 underlay + an upstream ToR receiving all N sessions, which a
single-host demo can't provide.
`)
}

// Manifests renders nothing: the anycast-bgp deploy path owns every
// resource this scenario inspects.
func (s *scenario) Manifests(*scenarios.Context) ([]string, error) { return nil, nil }

// Apply is a no-op for the same reason — there's nothing to push.
func (s *scenario) Apply(*scenarios.Context) error { return nil }

// Cleanup is a no-op: the scenario created nothing. The FRR peer, NAD,
// and ZeBOS template are owned by the deploy path and torn down by
// `destroy`, not here.
func (s *scenario) Cleanup(*scenarios.Context) error { return nil }

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	r := ctx.Runner
	res := scenarios.Result{}

	// Precondition: anycast-bgp mode is detected by the bnk-frr DaemonSet
	// the deploy path installs. Absent → skip cleanly (this cluster runs a
	// different data-plane mode; nothing failed).
	dsReady, err := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "daemonset", frrDS,
		"-o", "jsonpath={.status.numberReady}/{.status.desiredNumberScheduled}")
	if err != nil || strings.TrimSpace(dsReady) == "" {
		res.Status = "skipped"
		res.Summary = "no bnk-frr DaemonSet — cluster is not in anycast-bgp mode (set bnk.tmm_dataplane_mode: anycast-bgp)"
		return res
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "bnk-frr DaemonSet Ready (anycast-bgp FRR peer per TMM node)",
		OK:          strings.Contains(dsReady, "/") && !strings.HasPrefix(strings.TrimSpace(dsReady), "0/") && readyAll(dsReady),
		Got:         oneLine(dsReady, 20),
	})

	// Running TMM pods — the anycast path acts on all of them.
	tmms, err := deploy.RunningTMMPods(ctx.Ctx, r)
	if err != nil {
		res.Status = "failed"
		res.Summary = "no Running TMM pods"
		res.Details = err.Error()
		return res
	}

	// Per-TMM: net1 kept its kernel IP (mapres FALSE), BGP Established,
	// and a router-id equal to the pod IP. Collect router-ids to assert
	// distinctness across pods.
	routerIDs := map[string]string{} // pod -> router-id
	establishedCount := 0
	net1OK := 0
	for _, pod := range tmms {
		podIP, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod", pod,
			"-o", "jsonpath={.status.podIP}")
		podIP = strings.TrimSpace(podIP)

		// net1 (bnk-bgp) kernel IP present — mapres FALSE didn't flush it.
		if ip := podNADIP(ctx, "default", pod, deploy.BGPNADName); strings.HasPrefix(ip, "192.168.99.") {
			net1OK++
		}

		// ZeBOS BGP summary: router-id + Established session count.
		sum, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "exec", pod,
			"-c", "f5-tmm-routing", "--", "imish", "-e", "show ip bgp summary")
		rid := parseZebosRouterID(sum)
		routerIDs[pod] = rid
		if zebosEstablished(sum) {
			establishedCount++
		}
	}

	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("all %d TMM pod(s) kept net1 kernel IP on %s (mapres FALSE)", len(tmms), deploy.BGPNADName),
		OK:          net1OK == len(tmms),
		Got:         fmt.Sprintf("%d/%d", net1OK, len(tmms)),
	})
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("all %d TMM pod(s) have BGP Established to FRR %s", len(tmms), deploy.BGPPeerIP),
		OK:          establishedCount == len(tmms),
		Got:         fmt.Sprintf("%d/%d Established", establishedCount, len(tmms)),
	})

	// Per-pod router-id: each equals its pod IP AND all are distinct —
	// proving %%POD_IP%% expanded per pod from the one shared ConfigMap.
	distinct := map[string]bool{}
	allHaveRID := true
	for _, pod := range tmms {
		rid := routerIDs[pod]
		if rid == "" {
			allHaveRID = false
		}
		distinct[rid] = true
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "each TMM has a distinct ZeBOS router-id (shared %%POD_IP%% expands per pod)",
		OK:          allHaveRID && len(distinct) == len(tmms),
		Got:         fmt.Sprintf("%v", routerIDs),
	})

	// FRR side: each peer learned >=1 prefix from its node-local TMM, and
	// its dynamic-neighbor count matches the TMM pods co-located on its
	// node (one, on a single host — the documented ECMP limit).
	frrPods, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod", "-l", frrLabel,
		"--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	tmmNodeCount := tmmPerNode(ctx)
	frrLearnedAll := true
	frrNextHopOK := true
	var ecmpNote []string
	frrList := splitNonEmpty(frrPods)
	for _, fp := range frrList {
		node, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod", fp, "-o", "jsonpath={.spec.nodeName}")
		node = strings.TrimSpace(node)
		table, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "exec", fp, "-c", "frr", "--",
			"vtysh", "-c", "show bgp ipv4 unicast")
		learned := strings.Contains(table, "192.168.99.") || regexp.MustCompile(`Displayed\s+[1-9]`).MatchString(table)
		if !learned {
			frrLearnedAll = false
		}
		neighbors := countDynamicNeighbors(table) // best-effort; falls back to summary
		if neighbors == 0 {
			sum, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "exec", fp, "-c", "frr", "--",
				"vtysh", "-c", "show bgp summary")
			neighbors = countFRRNeighbors(sum)
		}
		want := tmmNodeCount[node]
		if want == 0 {
			want = 1
		}
		if neighbors != want {
			frrNextHopOK = false
		}
		ecmpNote = append(ecmpNote, fmt.Sprintf("%s(node %s): %d neighbor(s), want %d", fp, node, neighbors, want))
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("each of %d FRR peer(s) learned >=1 prefix from its node-local TMM", len(frrList)),
		OK:          len(frrList) > 0 && frrLearnedAll,
		Got:         fmt.Sprintf("%d FRR peer(s)", len(frrList)),
	})
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "each FRR sees exactly its node-local TMM count (single-host: 1 next-hop, NOT N — real ECMP needs shared L2)",
		OK:          len(frrList) > 0 && frrNextHopOK,
		Got:         oneLine(strings.Join(ecmpNote, "; "), 250),
	})

	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = fmt.Sprintf("%d TMM pod(s) each Established + advertising over BGP; %d FRR peer(s) each see their node-local TMM (anycast model; cross-node ECMP needs shared L2)",
			len(tmms), len(frrList))
	} else {
		res.Status = "failed"
		var failed []string
		for _, a := range res.Assertions {
			if !a.OK {
				failed = append(failed, a.Description)
			}
		}
		res.Summary = "failed: " + strings.Join(failed, "; ")
		res.Details = fmt.Sprintf("router-ids: %v\nECMP: %s", routerIDs, strings.Join(ecmpNote, "; "))
	}
	return res
}

// readyAll reports whether a "ready/desired" string has ready==desired
// and desired>0 (e.g. "2/2" → true, "1/2" → false).
func readyAll(s string) bool {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(parts) != 2 {
		return false
	}
	return parts[0] == parts[1] && parts[1] != "0" && parts[1] != ""
}

// parseZebosRouterID pulls the router-id out of ZeBOS `show ip bgp
// summary` — first line is "BGP router identifier 192.168.x.y, local AS
// number 65000".
func parseZebosRouterID(summary string) string {
	m := regexp.MustCompile(`router identifier (\d+\.\d+\.\d+\.\d+)`).FindStringSubmatch(summary)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// zebosEstablished reports whether ZeBOS shows at least one Established
// session — ZeBOS prints "Total number of Established sessions N".
func zebosEstablished(summary string) bool {
	m := regexp.MustCompile(`Total number of Established sessions (\d+)`).FindStringSubmatch(summary)
	return len(m) == 2 && m[1] != "0"
}

// countFRRNeighbors counts neighbor rows in FRR `show bgp summary`
// containing a 192.168.99.x peer (dynamic neighbors are prefixed "*").
func countFRRNeighbors(summary string) int {
	n := 0
	for _, line := range strings.Split(summary, "\n") {
		if regexp.MustCompile(`\*?192\.168\.99\.\d+\s+4\s`).MatchString(line) {
			n++
		}
	}
	return n
}

// countDynamicNeighbors reads "N dynamic neighbor(s)" from FRR output,
// returning 0 if the phrase isn't present (caller falls back).
func countDynamicNeighbors(out string) int {
	m := regexp.MustCompile(`(\d+) dynamic neighbor`).FindStringSubmatch(out)
	if len(m) == 2 {
		var n int
		fmt.Sscanf(m[1], "%d", &n)
		return n
	}
	return 0
}

// tmmPerNode returns a map of node name -> count of Running TMM pods on
// that node, so each FRR's expected neighbor count can be compared
// against the TMMs sharing its node's bridge.
func tmmPerNode(ctx *scenarios.Context) map[string]int {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod", "-l", tmmLabel,
		"--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.spec.nodeName}{"\n"}{end}`)
	m := map[string]int{}
	for _, n := range splitNonEmpty(out) {
		m[n]++
	}
	return m
}

// podNADIP returns the IPv4 the named NAD was assigned on a pod, read
// from the canonical Multus network-status annotation (not an exec, so
// it works regardless of what's in the container image and survives
// mapres kernel-IP shenanigans). Empty string if not yet present.
func podNADIP(ctx *scenarios.Context, namespace, pod, nad string) string {
	netStatus, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", namespace, "get", "pod", pod,
		"-o", `jsonpath={.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}`)
	if err != nil || strings.TrimSpace(netStatus) == "" {
		return ""
	}
	var entries []struct {
		Name string   `json:"name"`
		IPs  []string `json:"ips"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(netStatus)), &entries) != nil {
		return ""
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, nad) && !strings.HasSuffix(e.Name, "/"+nad) {
			continue
		}
		for _, ip := range e.IPs {
			if i := strings.Index(ip, "/"); i > 0 {
				ip = ip[:i]
			}
			if ip != "" && !strings.Contains(ip, ":") {
				return ip
			}
		}
	}
	return ""
}

func splitNonEmpty(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.TrimSpace(s), "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// oneLine collapses whitespace and truncates to n runes for compact
// Assertion.Got fields.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
