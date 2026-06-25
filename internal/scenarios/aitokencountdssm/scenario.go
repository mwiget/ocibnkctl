// Package aitokencountdssm implements scenario "ai-token-counting-dssm" — AI
// token counting delivered by a CUSTOM iRule that persists per-LB, per-mode
// token counters to DSSM/Redis, where they can be scraped for
// Prometheus/Grafana (the TMM AI Token Usage dashboard in tmmscope).
//
// This is the custom-iRule complement to the annotation-driven
// "ai-token-counting" scenario (how-to #6). Rather than the built-in
// k8s.f5.com/ai-token-counting Gateway annotation, it keeps explicit
// cumulative counters in an iRule `table` subtable. On full-fat BNK 2.3 the
// `table` command persists to the cluster's built-in DSSM/Redis
// (SESSIONDB_EXTERNAL_STORAGE=true, wired by the standard install — no extra
// deploy step), which makes the counter the durable source of truth a
// dashboard's day/week/month/year tiles can read. It also counts STREAMING
// (SSE) responses, which the request-scoped annotation path doesn't surface as
// a persisted counter.
//
// Shape:
//
//   - stands up FOUR LBs (Gateways/VIPs) sharing one llm-d-inference-sim backend
//   - attaches one F5BigCneIrule (04-irule.yaml) to each HTTP listener that, on
//     HTTP_RESPONSE / HTTP_RESPONSE_DATA, parses the OpenAI usage block, detects
//     streaming vs non-streaming from the response Content-Type, and
//     table-incr's cumulative total/prompt/completion counters keyed by
//     vs + mode + model. (An HTTP listener is required: an L4Route yields a
//     FastL4 VS that never runs the payload iRule.)
//   - drives ~60s of mixed streaming/non-streaming traffic across all four LBs
//   - reads the counters back out of DSSM/Redis (the built-in f5-dssm-db pod)
//     and asserts they match the backend-reported usage exactly (per LB, per
//     mode)
//
// Green: counting is hard-asserted end to end (DSSM read-back == backend usage),
// including streaming.
package aitokencountdssm

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

const (
	scnName  = "ai-token-counting-dssm"
	scnTitle = "AI Token Counting via custom iRule + DSSM/Redis — 4 LBs, streaming + non-streaming, persisted for tmm-stat-exporter"
	ns       = "scn-aitok-dssm"
	subtable = "TMMTOK"
)

// lb is one load balancer: a friendly name (the vs label the iRule maps the VIP
// to) and its Gateway VIP.
type lb struct {
	name string
	vip  string
}

var lbs = []lb{
	{"llm-chat", "203.0.113.120"},
	{"llm-code", "203.0.113.121"},
	{"llm-embed", "203.0.113.122"},
	{"llm-rag", "203.0.113.123"},
}

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }

// Dependencies: bgp-peer-frr brings up the FRR<->TMM BGP session the VIP /32s
// are advertised over. DSSM is part of the standard BNK install (the f5-dssm-db
// pods), so it isn't a scenario dependency; if it were ever missing, the "DSSM
// readable" assertion fails with a clear message rather than masquerading as a
// dependency.
func (s *scenario) Dependencies() []string { return []string{"bgp-peer-frr"} }

func (s *scenario) Description() string {
	return strings.TrimSpace(`
Deliver AI token counting with a custom iRule whose counters persist to the
built-in DSSM/Redis — the complement to the annotation-driven ai-token-counting
scenario, and the path that also counts STREAMING responses and persists durably
to Redis.

Four LBs (Gateways with an HTTP listener on 203.0.113.120-123) share one
llm-d-inference-sim backend. One F5BigCneIrule (04-irule.yaml), bound to each
HTTP listener via BNKNetPolicy, fires on HTTP_RESPONSE / HTTP_RESPONSE_DATA,
parses the OpenAI usage block, detects streaming vs non-streaming from the
response Content-Type, and table-incr's cumulative total/prompt/completion
counters keyed by vs + mode + model (an L4Route would yield a FastL4 VS that
never runs the payload iRule). With DSSM up (SESSIONDB_EXTERNAL_STORAGE=true, default on full BNK) the
table persists to Redis, where tmm-stat-exporter can emit
f5tmm_token_*{vs,mode,model} for a dashboard.

Verify drives ~60s of mixed streaming/non-streaming traffic across all four LBs,
then reads the counters back out of DSSM and HARD-ASSERTS they equal the
backend-reported usage for every LB and both modes. Cleanup deletes the
namespace.
`)
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	var paths []string
	err := fs.WalkDir(manifestFS, "manifests", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, e := manifestFS.ReadFile(p)
		if e != nil {
			return e
		}
		out, e := scenarios.WriteManifest(ctx.PoCDir, scnName, p[len("manifests/"):], string(body))
		if e != nil {
			return e
		}
		paths = append(paths, out)
		return nil
	})
	return paths, err
}

func (s *scenario) Apply(ctx *scenarios.Context) error {
	r := ctx.Runner
	for _, f := range []string{
		"01-namespace.yaml",
		"02-bnkgateway.yaml",
		"03-backend.yaml",
		"04-irule.yaml",
		"05-lb-chat.yaml",
		"06-lb-code.yaml",
		"07-lb-embed.yaml",
		"08-lb-rag.yaml",
	} {
		body, err := manifestFS.ReadFile("manifests/" + f)
		if err != nil {
			return err
		}
		if err := r.Apply(ctx.Ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}
	return nil
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	r := ctx.Runner
	res := scenarios.Result{}

	add := func(desc string, ok bool, got string) {
		res.Assertions = append(res.Assertions, scenarios.Assertion{Description: desc, OK: ok, Got: got})
	}

	// Backend + control plane.
	{
		err := r.Wait(ctx.Ctx, ns, "Available", "deployment/stub-llm", 2*time.Minute)
		add("stub-llm Deployment Available", err == nil, errString(err))
	}
	for _, l := range lbs {
		err := r.Kubectl(ctx.Ctx, "-n", ns, "wait", "--for=condition=Programmed",
			"--timeout=3m", "gateway/aitok-"+l.name+"-gw")
		add(fmt.Sprintf("Gateway aitok-%s-gw Programmed=True", l.name), err == nil, errString(err))
	}
	irule, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get",
		"f5-big-cne-irule/aitok-counter", "-o", "jsonpath={.metadata.name}")
	add("F5BigCneIrule aitok-counter exists", strings.TrimSpace(irule) == "aitok-counter", strings.TrimSpace(irule))

	// FRR learns all four VIPs via BGP.
	for _, l := range lbs {
		learned := waitBGP(ctx, l.vip, 2*time.Minute)
		add(fmt.Sprintf("FRR BGP table has %s/32 (LB %s)", l.vip, l.name), learned, fmt.Sprintf("learned=%v", learned))
	}

	// Baseline the DSSM counters before driving traffic, so the assertion is a
	// delta (idempotent across re-runs and robust to any pre-existing counters).
	before, _ := readDSSM(ctx)

	// Drive ~60s of mixed streaming/non-streaming traffic across all four LBs,
	// accumulating the backend-reported usage per (vs, mode).
	backend := map[string]int{} // "vs|mode|kind" -> summed tokens
	const rounds = 10
	reqOK := 0
	reqTotal := 0
	for round := 0; round < rounds; round++ {
		stream := round%2 == 1
		mode := "non-streaming"
		if stream {
			mode = "streaming"
		}
		for _, l := range lbs {
			reqTotal++
			pr, co, tot, ok := drive(ctx, l, round, stream)
			if !ok {
				continue
			}
			reqOK++
			backend[l.name+"|"+mode+"|total"] += tot
			backend[l.name+"|"+mode+"|prompt"] += pr
			backend[l.name+"|"+mode+"|completion"] += co
		}
	}
	add(fmt.Sprintf("traffic served: %d/%d requests returned a usage block", reqOK, reqTotal),
		reqOK == reqTotal, fmt.Sprintf("%d/%d", reqOK, reqTotal))

	// Give the iRule's table writes a moment to flush to DSSM, then read back
	// and compute the delta this run produced.
	time.Sleep(3 * time.Second)
	after, derr := readDSSM(ctx)
	add("DSSM/Redis readable (TMMTOK subtable scanned)", derr == nil, errString(derr))
	dssm := map[string]int{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			dssm[k] = d
		}
	}

	// Every LB counted, both modes present.
	countedLBs := map[string]bool{}
	modes := map[string]bool{}
	for key := range dssm {
		// key = "vs|mode|kind"
		parts := strings.Split(key, "|")
		if len(parts) == 3 && parts[2] == "total" {
			countedLBs[parts[0]] = true
			modes[parts[1]] = true
		}
	}
	add(fmt.Sprintf("all %d LBs have token counters in DSSM", len(lbs)),
		len(countedLBs) == len(lbs), fmt.Sprintf("counted=%d/%d %v", len(countedLBs), len(lbs), sortedKeys(countedLBs)))
	add("both streaming and non-streaming counted (mode detection works)",
		modes["streaming"] && modes["non-streaming"], fmt.Sprintf("%v", sortedKeys(modes)))

	// The load-bearing assertion: DSSM counters EQUAL the backend-reported usage
	// for every (vs, mode, kind) bucket that saw traffic — i.e. the iRule counted
	// exactly, streaming included.
	var mism []string
	for key, want := range backend {
		if got := dssm[key]; got != want {
			mism = append(mism, fmt.Sprintf("%s: dssm=%d backend=%d", key, got, want))
		}
	}
	sort.Strings(mism)
	add("DSSM counters match backend usage exactly (per LB, per mode, incl. streaming)",
		len(mism) == 0, oneLine(strings.Join(mism, "; "), 300))

	res.Details = renderBreakdown(backend, dssm)
	return finalize(res)
}

// drive sends one chat/completions request to lb (streaming or not) and returns
// the backend-reported prompt/completion/total token usage.
func drive(ctx *scenarios.Context, l lb, round int, stream bool) (prompt, completion, total int, ok bool) {
	content := fmt.Sprintf("token counting request round %d for %s with several words to tokenize", round, l.name)
	var payload string
	extra := []string{
		"-H", "Content-Type: application/json",
		"-H", "Host: " + l.name + ".aitok.local",
	}
	if stream {
		payload = fmt.Sprintf(`{"model":"gpt-stub","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":%q}]}`, content)
		extra = append(extra, "-N")
	} else {
		payload = fmt.Sprintf(`{"model":"gpt-stub","messages":[{"role":"user","content":%q}]}`, content)
	}
	extra = append(extra, "-d", payload)
	body, err := scenarios.FRRNetnsCurl(ctx, "http://"+l.vip+":8000/v1/chat/completions", extra...)
	if err != nil {
		return 0, 0, 0, false
	}
	tot := intAfter(body, `"total_tokens":`)
	if tot == 0 {
		return 0, 0, 0, false
	}
	return intAfter(body, `"prompt_tokens":`), intAfter(body, `"completion_tokens":`), tot, true
}

// readDSSM scans the TMMTOK subtable out of DSSM/Redis (via redis-cli in the
// f5-dssm-db pod) and returns a map "vs|mode|kind" -> token count.
func readDSSM(ctx *scenarios.Context) (map[string]int, error) {
	const rcli = `redis-cli --tls --cert /tls/dssm/mds/svr/tls.crt --key /tls/dssm/mds/svr/tls.key --cacert /tls/dssm/mds/svr/ca.crt -p 6379 -n 0`
	script := rcli + ` --scan --pattern '*` + subtable + `*' | while read k; do v=$(` + rcli + ` GET "$k" 2>/dev/null); [ -n "$v" ] && echo "$k~~~$v"; done`
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "exec", "f5-dssm-db-0", "--",
		"sh", "-c", script)
	if err != nil {
		return nil, err
	}
	res := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		k, v, found := strings.Cut(line, "~~~")
		if !found {
			continue
		}
		i := strings.Index(k, subtable)
		if i < 0 {
			continue
		}
		member := k[i+len(subtable):] // e.g. total|vs=llm-chat|mode=streaming|model=gpt-stub
		kind, labels, ok := strings.Cut(member, "|")
		if !ok {
			continue
		}
		var vs, mode string
		for _, p := range strings.Split(labels, "|") {
			lk, lv, ok := strings.Cut(p, "=")
			if !ok {
				continue
			}
			switch lk {
			case "vs":
				vs = lv
			case "mode":
				mode = lv
			}
		}
		m := valueRe.FindStringSubmatch(v)
		if vs == "" || mode == "" || m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		res[vs+"|"+mode+"|"+kind] += n
	}
	return res, nil
}

var valueRe = regexp.MustCompile(`S([0-9]+)$`)

func renderBreakdown(backend, dssm map[string]int) string {
	keys := sortedKeys(unionKeys(backend, dssm))
	var b strings.Builder
	b.WriteString("per-bucket (vs|mode|kind) dssm vs backend:\n")
	for _, k := range keys {
		mark := "ok"
		if backend[k] != dssm[k] {
			mark = "MISMATCH"
		}
		fmt.Fprintf(&b, "  %-40s dssm=%-6d backend=%-6d %s\n", k, dssm[k], backend[k], mark)
	}
	return strings.TrimRight(b.String(), "\n")
}

// waitBGP polls the external FRR's BGP table until vip/32 appears, re-issuing
// the OcNOS redistribute on every TMM each iteration (full BNK only injects a
// redistributed kernel route into BGP when the statement is re-issued at runtime
// after the route exists — see scenarios.RetriggerRedistribute).
func waitBGP(ctx *scenarios.Context, vip string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		scenarios.RetriggerRedistribute(ctx)
		if t, _ := scenarios.FRRVtysh(ctx, "show bgp ipv4 unicast"); strings.Contains(t, vip) {
			return true
		}
		select {
		case <-ctx.Ctx.Done():
			return false
		case <-time.After(5 * time.Second):
		}
	}
	return false
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	_ = ctx.Runner.Kubectl(ctx.Ctx, "delete", "namespace", ns, "--ignore-not-found")
	return nil
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "4 LBs counting AI tokens via custom iRule -> DSSM/Redis; counters match backend usage (streaming + non-streaming)"
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

// --- small helpers ---

func intAfter(s, key string) int {
	i := strings.Index(s, key)
	if i < 0 {
		return 0
	}
	j := i + len(key)
	for j < len(s) && s[j] == ' ' {
		j++
	}
	k := j
	for k < len(s) && s[k] >= '0' && s[k] <= '9' {
		k++
	}
	if k == j {
		return 0
	}
	n, _ := strconv.Atoi(s[j:k])
	return n
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionKeys(a, b map[string]int) map[string]bool {
	u := map[string]bool{}
	for k := range a {
		u[k] = true
	}
	for k := range b {
		u[k] = true
	}
	return u
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return oneLine(err.Error(), 200)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
