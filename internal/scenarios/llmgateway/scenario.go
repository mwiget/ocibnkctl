// Package llmgateway implements scenario "llm-gateway" — BNK 2.4.1's
// AI/LLM gateway end to end, the demo a field engineer shows.
//
// It needs bnk.ai_gateway.enabled (FLO then installs the console, the AI
// gateway API in f5-cne-controller and, with ai_metrics, ClickHouse). On top
// of that it stands up a GPU-free OpenAI-compatible mock LLM, configures the
// gateway through its REST API exactly as an operator would in the console
// (tenant, provider key, model, principal, route, virtual key), then sends
// chat completions through TMM from the external edge and checks what the
// gateway enforced and what it metered.
//
// The API calls live in manifests/llm-setup.sh, run as an in-cluster Job, so
// the same script is the documented manual path (docs/llm-gateway-howto.md).
package llmgateway

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
	"time"

	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

//go:embed manifests/*
var manifestFS embed.FS

const (
	scnName  = "llm-gateway"
	scnTitle = "LLM gateway (BNK 2.4.1 AI gateway) — virtual keys, model scope, metering, console"

	ns          = "llm-demo"
	setupScript = "llm-setup.sh"

	// Tenant and VIP are fixed so the printed curl, the docs and the deck all
	// match. 203.0.113.140 is outside every other scenario's range.
	Tenant = "demo"
	VIP    = "203.0.113.140"
	Model  = "gpt-4o-mini"
	// Route is the name llm-setup.sh gives the route (and so its Gateway,
	// HTTPRoute, AppPolicy and AIGatewayProfile): tenant-qualified, because
	// every tenant's objects share the default namespace.
	Route = Tenant + "-llm"

	mockURL = "http://mock-llm." + ns + ".svc:8080/v1"
)

// Endpoint is where the scenario writes the demo's virtual key, under the PoC
// dir, so `scenario run` can print a ready-to-paste curl. It is the one
// secret the scenario creates; it is mode 0600 and keys/ stays untouched.
const Endpoint = "artifacts/llm-gateway/virtual-key"

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{"bgp-peer-frr"} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
The BNK 2.4.1 AI/LLM gateway end to end. Requires bnk.ai_gateway.enabled
(FLO installs the console, the AI gateway API and, with ai_metrics,
ClickHouse) and the anycast-bgp data plane (clients reach the Gateway VIP
from the external edge over BGP).

Apply: deploys mock-llm (llm-d-inference-sim, OpenAI-compatible, no GPU, no
API key) in llm-demo, then runs llm-setup.sh as a Job against the AI gateway
API: tenant "demo", provider key → mock, model gpt-4o-mini, principal
demo-app, route demo-llm on 203.0.113.140:80 (Gateway + HTTPRoute + AppPolicy
+ AIGatewayProfile), and a virtual key scoped to gpt-4o-mini.

Verify, from the edge FRR's netns (a client outside the cluster):
  - the virtual key gets a 200 chat completion with a usage block
  - a wrong key gets 401, no key gets 401
  - a model outside the key's scope gets 403
  - with ai_metrics, ClickHouse holds one record per request
  - the route's AIGatewayProfile reports Programmed

Works around an f5ingress 2.4.1 race: a new route's Gateway apply can fail
with "AI profile ... is not prepared yet" and is not retried, so TMM never
gets the virtual server. Verify restarts f5-cne-controller once when it sees
that state.

Cleanup deletes llm-demo. The tenant and its objects stay (the API owns
them); delete them in the console or with the API.
`)
}

type inputs struct {
	Tenant, VIP, Model, ProviderURL string
}

func defaults() inputs {
	return inputs{Tenant: Tenant, VIP: VIP, Model: Model, ProviderURL: mockURL}
}

// render fills a manifest's {{.Field}} placeholders. The setup script is
// shell, not a template, and is copied as is.
func render(name string, in inputs) (string, error) {
	raw, err := manifestFS.ReadFile("manifests/" + name)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(name, ".yaml") {
		return string(raw), nil
	}
	t, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, in); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	var paths []string
	err := fs.WalkDir(manifestFS, "manifests", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		base := strings.TrimPrefix(p, "manifests/")
		body, e := render(base, defaults())
		if e != nil {
			return e
		}
		out, e := scenarios.WriteManifest(ctx.PoCDir, scnName, base, body)
		if e != nil {
			return e
		}
		paths = append(paths, out)
		return nil
	})
	return paths, err
}

// requireAIGateway fails fast when the PoC has no AI gateway, instead of the
// setup Job timing out against a Service that does not exist.
func requireAIGateway(ctx *scenarios.Context) error {
	if ctx.PoC.BNK.AIGateway.Enabled {
		return nil
	}
	return fmt.Errorf("%s needs bnk.ai_gateway.enabled: true in poc.yaml (then `ocibnkctl deploy cne`), "+
		"or start from `ocibnkctl init <name> --profile llm-gateway`", scnName)
}

func (s *scenario) Apply(ctx *scenarios.Context) error {
	if err := requireAIGateway(ctx); err != nil {
		return err
	}
	if err := scenarios.RequireAnycastBGP(ctx, scnName); err != nil {
		return err
	}
	r := ctx.Runner
	mock, err := render("01-mock-llm.yaml", defaults())
	if err != nil {
		return err
	}
	if err := r.Apply(ctx.Ctx, mock); err != nil {
		return fmt.Errorf("apply mock-llm: %w", err)
	}
	if err := r.Wait(ctx.Ctx, ns, "Available", "deployment/mock-llm", 3*time.Minute); err != nil {
		return fmt.Errorf("mock-llm not Available: %w", err)
	}

	// The Job reads the platform admin password and the provider key from
	// Secrets in its own namespace; neither ever appears on a command line.
	pw, err := platformAdminPassword(ctx)
	if err != nil {
		return err
	}
	if err := applySecret(ctx, "llm-admin", "password", pw); err != nil {
		return err
	}
	if err := applySecret(ctx, "llm-provider", "api-key", "sk-mock"); err != nil {
		return err
	}
	script, err := render(setupScript, defaults())
	if err != nil {
		return err
	}
	if err := r.Apply(ctx.Ctx, configMap("llm-setup", setupScript, script)); err != nil {
		return fmt.Errorf("apply llm-setup ConfigMap: %w", err)
	}

	// A Job's pod template is immutable: replace any previous run.
	_ = r.Kubectl(ctx.Ctx, "-n", ns, "delete", "job", "llm-setup", "--ignore-not-found", "--wait=true")
	job, err := render("02-setup-job.yaml", defaults())
	if err != nil {
		return err
	}
	if err := r.Apply(ctx.Ctx, job); err != nil {
		return fmt.Errorf("apply llm-setup Job: %w", err)
	}
	logs, err := waitJob(ctx, "llm-setup", 3*time.Minute)
	fmt.Fprint(ctx.Out, indent(redactKey(logs)))
	if err != nil {
		return fmt.Errorf("llm-setup Job: %w", err)
	}
	key := virtualKey(logs)
	if key == "" {
		return fmt.Errorf("llm-setup printed no virtual key")
	}
	return writeKey(ctx, key)
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	res := scenarios.Result{}
	if err := requireAIGateway(ctx); err != nil {
		res.Status, res.Summary = "failed", err.Error()
		return res
	}
	key, err := readKey(ctx)
	if err != nil {
		res.Status, res.Summary = "failed", "no virtual key from Apply: "+err.Error()
		return res
	}

	// 1. TMM has the route's virtual server. Restart the controller once if
	// the 2.4.1 "profile not prepared" race left it out.
	vsOK, restarted := ensureVirtualServer(ctx)
	got := "present"
	if restarted {
		got = "present after restarting f5-cne-controller (f5ingress 2.4.1 profile race)"
	}
	if !vsOK {
		got = "missing — " + profileProgrammed(ctx)
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("TMM has the %s virtual server on %s:80", Route, VIP),
		OK:          vsOK, Got: got,
	})

	// 2. The edge learns the VIP over BGP.
	learned := waitVIPAdvertised(ctx, 2*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("edge FRR learned %s/32 from TMM over BGP", VIP),
		OK:          learned, Got: fmt.Sprintf("learned=%v", learned),
	})

	// 3. What the gateway enforces, seen from a client outside the cluster.
	for _, c := range []struct {
		desc, key, model string
		code             int
		body             string
	}{
		{"virtual key + allowed model → 200 with a usage block", key, Model, 200, `"usage"`},
		{"wrong key → 401", "sk-not-a-virtual-key", Model, 401, "Invalid virtual key"},
		{"no key → 401", "", Model, 401, "Authorization"},
		{"model outside the key's scope → 403", key, "gpt-5", 403, "Model not allowed"},
	} {
		code, body := chat(ctx, c.key, c.model)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: c.desc,
			OK:          code == c.code && strings.Contains(body, c.body),
			Got:         fmt.Sprintf("HTTP %d %s", code, oneLine(body, 160)),
		})
	}

	// 4. Metering: one ClickHouse record per request, rejects included.
	if ctx.PoC.BNK.AIGateway.AIMetrics {
		ok, summary := meteredRecords(ctx)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "ClickHouse otel.aigw_requests has records for tenant demo (200, 401, 403)",
			OK:          ok, Got: summary,
		})
	}

	res = finalize(res)
	if res.Status == "ok" {
		res.Details = tryIt(ctx)
		fmt.Fprint(ctx.Out, res.Details)
	}
	return res
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	_ = ctx.Runner.Kubectl(ctx.Ctx, "delete", "namespace", ns, "--ignore-not-found")
	return nil
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "LLM gateway enforced keys and model scope through TMM and metered every request"
		return res
	}
	res.Status = "failed"
	var failed []string
	for _, a := range res.Assertions {
		if !a.OK {
			failed = append(failed, a.Description)
		}
	}
	res.Summary = "failed: " + strings.Join(failed, "; ")
	return res
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if l != "" {
			b.WriteString("      | " + l + "\n")
		}
	}
	return b.String()
}
