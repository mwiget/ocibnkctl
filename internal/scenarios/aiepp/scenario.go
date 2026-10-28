// Package aiepp implements scenario "ai-inference-epp" — F5 BNK how-to
// "AI Inference Routing with the F5 Endpoint Picker" (new in BNK 2.4).
//
// The data path under test:
//
//	client ─► Gateway VIP (TMM) ─► HTTPRoute backendRef kind=InferencePool
//	                │ ext_proc gRPC consult (:9002)
//	                ▼
//	        F5 EPP (llm-pool-epp) ◄── scrapes /metrics ── mock-vllm-{busy,idle}
//	                │ returns the chosen pod IP
//	                ▼
//	        TMM forwards to that pod
//
// No GPUs or real model: two mock vLLM pods publish fixed vLLM Prometheus
// series. The "busy" pod reports a saturated queue and a 97% full KV cache,
// the "idle" pod reports nothing queued. Both match the InferencePool
// selector, so a load-blind balancer would split traffic between them; the
// scenario asserts every request lands on the idle pod, and that TMM's own
// counters (inference_epp_stats) show one successful EPP consult per request.
//
// Apply also does the two cluster-wide prerequisites the F5 doc lists:
// installs the GAIE v1.4.0 CRDs (not bundled with BNK) and adds scn-epp to
// CNEInstance.spec.eppNamespaces (empty means the controller ignores every
// F5EPP). Cleanup leaves both in place — see Cleanup.
package aiepp

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
	"github.com/mwiget/ocibnkctl/internal/version"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

const (
	scnName  = "ai-inference-epp"
	scnTitle = "AI Inference Routing with the F5 Endpoint Picker — F5EPP + GAIE InferencePool, mock vLLM"

	ns       = "scn-epp"
	pool     = "llm-pool"
	eppName  = "llm-pool-epp" // the F5EPP CR and, by the controller's naming contract, its Deployment + Service
	gwVIP    = "203.0.113.124"
	host     = "epp.ocibnkctl.local"
	requests = 10

	// model is what every request names. The EPP loads its tokenizer for
	// the request's model (not the pods' metrics) from HuggingFace, so it
	// must be an ungated repo that ships a tokenizer.json.
	model = "Qwen/Qwen2.5-0.5B-Instruct"
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{"bgp-peer-frr"} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
BNK 2.4's F5 Endpoint Picker: TMM consults a per-InferencePool EPP over
gRPC ext_proc before forwarding each request, and the EPP picks the
backend pod from its vLLM metrics (queue depth, running requests, KV-cache
use, prefix affinity).

Setup (the F5 doc's prerequisites, done by Apply):
  - GAIE ` + version.GAIEVersion + ` CRDs (InferencePool etc. — not bundled with BNK)
  - scn-epp added to CNEInstance.spec.eppNamespaces
  - two mock vLLM pods in one pool: "busy" (40 queued, KV cache 97%)
    and "idle" (nothing queued, KV cache 2%)
  - F5EPP (engine: vllm) → InferencePool (FailClose) → HTTPRoute on a
    Gateway at ` + gwVIP + `

Verify: the F5EPP reaches Available, the controller-rendered EPP
Deployment/Service exist, InferencePool and HTTPRoute resolve, and
` + strconv.Itoa(requests) + ` requests from the external FRR all land on the idle pod, with
TMM's inference_epp_stats showing one successful consult per request.

Not covered: real KV-cache events (the mock publishes no ZMQ stream, so
the picker uses speculative cache tracking), prefix-affinity scoring,
Dynamo/SGLang engines, InferenceObjective/InferenceModelRewrite.

Also checks the EPP tokenized every prompt (its prefix-index counters), since
a failed tokenizer load is otherwise silent. The requests name
` + model + `, whose tokenizer the EPP downloads from HuggingFace, so
the cluster needs internet access.
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
	if err := scenarios.RequireAnycastBGP(ctx, scnName); err != nil {
		return err
	}
	r := ctx.Runner

	// The F5EPP CRD ships with BNK 2.4+; fail fast with the reason on an
	// older release instead of an opaque "no matches for kind" later.
	if _, err := r.KubectlCapture(ctx.Ctx, "get", "crd", "f5-epps.inference.k8s.f5.com"); err != nil {
		return fmt.Errorf("F5EPP CRD not installed — the F5 Endpoint Picker needs BNK 2.4+: %w", err)
	}

	fmt.Fprintf(ctx.Out, "      | installing GAIE %s CRDs\n", version.GAIEVersion)
	if err := deploy.EnsureGAIECRDs(ctx.Ctx, r); err != nil {
		return err
	}

	if err := ensureEPPNamespace(ctx); err != nil {
		return err
	}

	for _, f := range []string{
		"01-namespace.yaml",
		"02-mock-vllm.yaml",
		"03-f5epp.yaml",
		"04-inferencepool.yaml",
		"05-gateway.yaml",
		"06-httproute.yaml",
	} {
		body, err := scenarios.ReadManifest(ctx.PoCDir, scnName, f)
		if err != nil {
			return err
		}
		if err := r.Apply(ctx.Ctx, body); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
	}
	return nil
}

// ensureEPPNamespace adds ns to CNEInstance.spec.eppNamespaces, keeping
// whatever is already listed. A merge patch replaces the whole list, so the
// current one is read first. No-op when ns or "All" is already there — the
// patch makes FLO roll the CNE controller, so skipping it saves a minute on
// re-runs.
func ensureEPPNamespace(ctx *scenarios.Context) error {
	r := ctx.Runner
	raw, err := r.KubectlCapture(ctx.Ctx, "-n", "default", "get", "cneinstance", "bnk-instance",
		"-o", "jsonpath={.spec.eppNamespaces}")
	if err != nil {
		return fmt.Errorf("read CNEInstance.spec.eppNamespaces: %w", err)
	}
	list, err := parseNamespaces(raw)
	if err != nil {
		return err
	}
	for _, n := range list {
		if n == ns || n == "All" {
			fmt.Fprintf(ctx.Out, "      | CNEInstance.spec.eppNamespaces already covers %s: %v\n", ns, list)
			return nil
		}
	}
	list = append(list, ns)
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"eppNamespaces": list}})
	fmt.Fprintf(ctx.Out, "      | CNEInstance.spec.eppNamespaces → %v\n", list)
	if err := r.Kubectl(ctx.Ctx, "-n", "default", "patch", "cneinstance", "bnk-instance",
		"--type=merge", "-p", string(patch)); err != nil {
		return fmt.Errorf("patch CNEInstance.spec.eppNamespaces: %w", err)
	}
	return nil
}

// parseNamespaces decodes the jsonpath rendering of a string list: empty
// when the field is unset, a JSON array otherwise.
func parseNamespaces(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("parse CNEInstance.spec.eppNamespaces %q: %w", raw, err)
	}
	return list, nil
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	r := ctx.Runner
	res := scenarios.Result{}
	add := func(desc string, ok bool, got string) {
		res.Assertions = append(res.Assertions, scenarios.Assertion{Description: desc, OK: ok, Got: got})
	}

	for _, d := range []string{"mock-vllm-busy", "mock-vllm-idle"} {
		err := r.Wait(ctx.Ctx, ns, "Available", "deployment/"+d, 3*time.Minute)
		add(d+" Deployment Available", err == nil, errString(err))
	}

	// The controller only reconciles F5EPPs after the eppNamespaces change
	// rolls it, then pulls the f5-epp image — allow for both.
	{
		err := r.Wait(ctx.Ctx, ns, "Available", "f5-epps.inference.k8s.f5.com/"+eppName, 8*time.Minute)
		conds, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get", "f5-epps.inference.k8s.f5.com", eppName,
			"-o", `jsonpath={range .status.conditions[*]}{.type}={.status} {end}`)
		for _, c := range []string{"Accepted", "ResolvedRefs", "Programmed", "Available"} {
			add("F5EPP condition "+c+"=True", strings.Contains(conds, c+"=True"), strings.TrimSpace(conds))
		}
		if err != nil {
			msg, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get", "f5-epps.inference.k8s.f5.com", eppName,
				"-o", `jsonpath={range .status.conditions[*]}{.type}: {.message}{"\n"}{end}`)
			res.Details = "F5EPP not Available:\n" + msg
		}
	}

	// Controller naming contract: <pool>-epp Service with fixed ports
	// 9002 ext_proc, 9003 health, 9090 metrics.
	ports, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get", "service", eppName,
		"-o", `jsonpath={.spec.ports[*].port}`)
	add("controller rendered Service "+eppName+" with ext_proc port 9002",
		strings.Contains(" "+ports+" ", " 9002 "), "ports="+strings.TrimSpace(ports))

	rbac, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get", "role,rolebinding", "-o", "name")
	add("controller rendered EPP Role/RoleBinding in "+ns+" (eppNamespaces took effect)",
		strings.Contains(rbac, "epp"), oneLine(rbac, 200))

	{
		err := r.Kubectl(ctx.Ctx, "-n", ns, "wait", "--for=condition=Programmed",
			"--timeout=3m", "gateway/scn-epp-gateway")
		add("Gateway Programmed=True", err == nil, errString(err))
	}

	routeConds := parentConditions(ctx, "httproute", "scn-epp-route")
	add("HTTPRoute Accepted=True", strings.Contains(routeConds, "Accepted=True"), routeConds)
	add("HTTPRoute ResolvedRefs=True (InferencePool backendRef resolved)",
		strings.Contains(routeConds, "ResolvedRefs=True"), routeConds)

	poolConds := parentConditions(ctx, "inferencepool.inference.networking.k8s.io", pool)
	add("InferencePool Accepted=True by the Gateway", strings.Contains(poolConds, "Accepted=True"), poolConds)
	add("InferencePool ResolvedRefs=True (endpointPickerRef resolved)",
		strings.Contains(poolConds, "ResolvedRefs=True"), poolConds)

	// The external FRR learns the VIP over BGP; the curls come from its netns.
	learned := false
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); {
		scenarios.RetriggerRedistribute(ctx)
		if tbl, _ := scenarios.FRRVtysh(ctx, "show bgp ipv4 unicast"); strings.Contains(tbl, gwVIP) {
			learned = true
			break
		}
		select {
		case <-ctx.Ctx.Done():
			return finalize(res)
		case <-time.After(5 * time.Second):
		}
	}
	add("FRR BGP table has "+gwVIP+"/32 advertised by TMM", learned, fmt.Sprintf("learned=%v", learned))

	// The first request for a model makes the EPP download its tokenizer,
	// which can outlast the per-request prepare-data timeout. That request
	// is still routed, just without prefix scoring, so warm up once before
	// the measured run.
	_, _ = scenarios.FRRNetnsRun(ctx, nil, "sh", "-c", curlScript(1, 30, "warm-up"))

	tmms := tmmPods(ctx)
	before := sumEPPStats(ctx, tmms)
	eppBefore := eppMetrics(ctx)

	// One throwaway container runs all requests in sequence, so the EPP's
	// in-flight tracking never sees two at once and the only thing that can
	// tell the pods apart is the metrics they publish.
	out, _ := scenarios.FRRNetnsRun(ctx, nil, "sh", "-c", curlScript(requests, 8, "request"))
	idle := strings.Count(out, "ocibnkctl-epp-role=idle")
	busy := strings.Count(out, "ocibnkctl-epp-role=busy")
	add(fmt.Sprintf("%d/%d requests through the Gateway answered by a mock vLLM pod", requests, requests),
		idle+busy == requests, fmt.Sprintf("answered=%d idle=%d busy=%d", idle+busy, idle, busy))
	add("EPP steered every request to the idle pod (a load-blind balancer would split ~50/50)",
		idle == requests && busy == 0, fmt.Sprintf("idle=%d busy=%d", idle, busy))
	if idle+busy != requests {
		res.Details += "\ncurl output: " + oneLine(out, 400)
	}

	// Tokenization is silent when it fails: the pick still happens, prefix
	// affinity just scores zero. The EPP's own counters tell the two apart:
	// a tokenized prompt makes a prefix-index lookup (hit or miss), an
	// untokenized one is counted as no_query / encode_failed.
	eppAfter := eppMetrics(ctx)
	delta := func(k string) float64 { return eppAfter[k] - eppBefore[k] }
	lookups := delta(`f5_epp_prefix_index_lookups_total{result="hit"}`) +
		delta(`f5_epp_prefix_index_lookups_total{result="miss"}`)
	failed := delta(`epp_tokenizer_guard_events_total{event="encode_failed"}`)
	add(fmt.Sprintf("EPP tokenized every prompt with the %s tokenizer (prefix-index lookups ≥ %d, no encode failures)", model, requests),
		lookups >= requests && failed == 0,
		fmt.Sprintf("Δlookups(hit+miss)=%.0f Δencode_failed=%.0f", lookups, failed))

	after := sumEPPStats(ctx, tmms)
	calls := after["schedule_calls"] - before["schedule_calls"]
	ok := after["schedule_successes"] - before["schedule_successes"]
	fallbacks := after["schedule_fallbacks"] - before["schedule_fallbacks"]
	stats := fmt.Sprintf("Δschedule_calls=%d Δschedule_successes=%d Δschedule_fallbacks=%d over %d TMM(s)",
		calls, ok, fallbacks, len(tmms))
	add(fmt.Sprintf("TMM consulted the EPP for every request (inference_epp_stats Δschedule_calls ≥ %d)", requests),
		calls >= requests, stats)
	add("every EPP consult succeeded, none fell back", calls > 0 && ok == calls && fallbacks == 0, stats)

	ivs := ns + "-" + pool + "-epp-ivs"
	found := false
	for _, p := range tmms {
		t, _ := r.KubectlCapture(ctx.Ctx, "-n", "default", "exec", p, "-c", "debug", "--",
			"tmctl", "-d", "blade", "profile_ext_proc_stat")
		if strings.Contains(t, ivs) {
			found = true
			break
		}
	}
	add("TMM profile_ext_proc_stat has the "+ivs+" internal virtual server", found,
		fmt.Sprintf("found=%v", found))

	return finalize(res)
}

// curlScript sends n chat completions naming model, one after another.
func curlScript(n, timeoutSec int, tag string) string {
	body := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"%s '$i'"}]}`, model, tag)
	return fmt.Sprintf(`for i in $(seq %d); do curl -sS --max-time %d -H 'Host: %s' `+
		`-H 'Content-Type: application/json' -d '%s' http://%s/v1/chat/completions; echo; done`,
		n, timeoutSec, host, body, gwVIP)
}

// eppMetrics scrapes the EPP's Prometheus endpoint through the API server's
// service proxy (no port-forward) and returns the series eppSeries parses.
func eppMetrics(ctx *scenarios.Context) map[string]float64 {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "get", "--raw",
		"/api/v1/namespaces/"+ns+"/services/"+eppName+":9090/proxy/metrics")
	return eppSeries(out)
}

// eppSeries sums the EPP's tokenizer/prefix counters by their one
// distinguishing label, dropping the per-instance labels so the keys stay
// stable across EPP pod restarts:
//
//	f5_epp_prefix_index_lookups_total{instance="…",pool="…",result="miss"} 2
//	→ f5_epp_prefix_index_lookups_total{result="miss"} = 2
func eppSeries(text string) map[string]float64 {
	keep := map[string]string{
		"f5_epp_prefix_index_lookups_total": "result",
		"epp_tokenizer_guard_events_total":  "event",
	}
	sum := map[string]float64{}
	for _, line := range strings.Split(text, "\n") {
		open := strings.IndexByte(line, '{')
		close := strings.LastIndexByte(line, '}')
		if open < 0 || close < open {
			continue
		}
		label, ok := keep[line[:open]]
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(line[close+1:]), 64)
		if err != nil {
			continue
		}
		for _, kv := range strings.Split(line[open+1:close], ",") {
			if strings.HasPrefix(kv, label+"=") {
				sum[line[:open]+"{"+kv+"}"] += v
			}
		}
	}
	return sum
}

// parentConditions renders status.parents[*].conditions of a Gateway API /
// GAIE object as "Type=Status" words.
func parentConditions(ctx *scenarios.Context, kind, name string) string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", ns, "get", kind, name,
		"-o", `jsonpath={range .status.parents[*].conditions[*]}{.type}={.status} {end}`)
	return strings.TrimSpace(out)
}

func tmmPods(ctx *scenarios.Context) []string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pods",
		"-l", "app=f5-tmm", "--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	return strings.Fields(out)
}

// sumEPPStats adds up inference_epp_stats across every TMM — the anycast
// Gateway VIP lands each request on whichever TMM ECMP picked.
func sumEPPStats(ctx *scenarios.Context, tmms []string) map[string]int64 {
	sum := map[string]int64{}
	for _, p := range tmms {
		out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "exec", p, "-c", "debug", "--",
			"tmctl", "-d", "blade", "inference_epp_stats")
		for k, v := range parseTmctlRow(out) {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				sum[k] += n
			}
		}
	}
	return sum
}

// parseTmctlRow maps column → value for the first data row of a tmctl
// table. tmctl wraps wide tables into stacked blocks, each a header line, a
// dashes line, then the rows:
//
//	name          schedule_calls schedule_successes
//	------------- -------------- ------------------
//	inference_epp              2                  2
//
// so every header line directly above a dashes line is paired with the
// line below it.
func parseTmctlRow(out string) map[string]string {
	lines := strings.Split(out, "\n")
	row := map[string]string{}
	for i := 1; i+1 < len(lines); i++ {
		if !isDashes(lines[i]) {
			continue
		}
		cols, vals := strings.Fields(lines[i-1]), strings.Fields(lines[i+1])
		if len(cols) != len(vals) {
			continue
		}
		for j, c := range cols {
			row[c] = vals[j]
		}
	}
	return row
}

func isDashes(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && strings.Trim(s, "- ") == ""
}

// Cleanup deletes the scenario namespace (F5EPP first, so the controller
// tears down the EPP stack it rendered while it still has RBAC there).
// It deliberately leaves two cluster-wide changes behind:
//   - scn-epp in CNEInstance.spec.eppNamespaces: removing it rolls the CNE
//     controller again, and naming an absent namespace is harmless.
//   - the GAIE CRDs: cluster-scoped and shared by anything else that uses
//     InferencePool; deleting them would delete every such object.
func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	r := ctx.Runner
	_ = r.Kubectl(ctx.Ctx, "-n", ns, "delete", "f5-epps.inference.k8s.f5.com", eppName,
		"--ignore-not-found", "--timeout=2m")
	_ = r.Kubectl(ctx.Ctx, "delete", "namespace", ns, "--ignore-not-found")
	return nil
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "F5 EPP reconciled; every request consulted the EPP and landed on the idle mock vLLM pod"
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
	res.Details = strings.TrimSpace(res.Details)
	return res
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
