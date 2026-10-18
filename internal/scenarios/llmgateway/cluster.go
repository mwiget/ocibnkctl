package llmgateway

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

// FLO's names for what it renders from CNEInstance.spec.aiGateway (AIGateway
// CR f5-aigateway, in the CNEInstance namespace).
const (
	floNS              = "default"
	platformAdmin      = "f5-aigateway-admin"
	clickHousePod      = "f5-aigateway-clickhouse-0"
	consoleService     = "f5-aigateway-console"
	controllerDeploy   = "f5-cne-controller"
	tenantAdminPattern = "aitenant-%s-admin" // the console's per-tenant sign-in
)

func secretField(ctx *scenarios.Context, name, key string) (string, error) {
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", floNS, "get", "secret", name,
		"-o", "jsonpath={.data."+key+"}")
	if err != nil {
		return "", err
	}
	v, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
	if err != nil {
		return "", fmt.Errorf("secret %s/%s: %w", name, key, err)
	}
	return string(v), nil
}

// platformAdminPassword is the API's top-level admin, which may create
// tenants. The console signs in per tenant instead (tenantAdminPattern).
func platformAdminPassword(ctx *scenarios.Context) (string, error) {
	pw, err := secretField(ctx, platformAdmin, "password")
	if err != nil || pw == "" {
		return "", fmt.Errorf("read %s (FLO creates it when bnk.ai_gateway is enabled): %v", platformAdmin, err)
	}
	return pw, nil
}

// applySecret creates or updates a one-key Secret in the demo namespace. The
// value only ever sits in 0600 temp files: kubectl renders the Secret from
// one and applies it from another, through KubectlCapture, which streams
// nothing. It is never in argv or in the scenario's output.
func applySecret(ctx *scenarios.Context, name, key, value string) error {
	dir, err := os.MkdirTemp("", "ocibnkctl-llm-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	val := filepath.Join(dir, key)
	if err := os.WriteFile(val, []byte(value), 0o600); err != nil {
		return err
	}
	manifest, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", ns, "create", "secret", "generic", name,
		"--from-file="+key+"="+val, "--dry-run=client", "-o", "yaml")
	if err != nil {
		return fmt.Errorf("render secret %s: %w", name, err)
	}
	m := filepath.Join(dir, "secret.yaml")
	if err := os.WriteFile(m, []byte(manifest), 0o600); err != nil {
		return err
	}
	if _, err := ctx.Runner.KubectlCapture(ctx.Ctx, "apply", "-f", m); err != nil {
		return fmt.Errorf("apply secret %s: %w", name, err)
	}
	return nil
}

func configMap(name, key, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  %s: |\n", name, ns, key)
	for _, l := range strings.Split(body, "\n") {
		b.WriteString("    " + l + "\n")
	}
	return b.String()
}

// waitJob waits for the Job to finish either way and returns its logs.
func waitJob(ctx *scenarios.Context, name string, timeout time.Duration) (string, error) {
	r := ctx.Runner
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "get", "job", name,
			"-o", "jsonpath={.status.succeeded}/{.status.failed}")
		logs, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "logs", "job/"+name)
		switch {
		case strings.HasPrefix(st, "1/"):
			return logs, nil
		case strings.HasSuffix(st, "/1"):
			return logs, fmt.Errorf("failed (see the log above)")
		}
		select {
		case <-ctx.Ctx.Done():
			return logs, ctx.Ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	logs, _ := r.KubectlCapture(ctx.Ctx, "-n", ns, "logs", "job/"+name)
	return logs, fmt.Errorf("not finished after %s", timeout)
}

var vkeyLine = regexp.MustCompile(`(?m)^VKEY=(\S+)\s*$`)

func virtualKey(logs string) string {
	if m := vkeyLine.FindStringSubmatch(logs); m != nil {
		return m[1]
	}
	return ""
}

func redactKey(logs string) string {
	return vkeyLine.ReplaceAllString(logs, "VKEY=<saved to "+Endpoint+">")
}

func writeKey(ctx *scenarios.Context, key string) error {
	p := filepath.Join(ctx.PoCDir, Endpoint)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(key+"\n"), 0o600)
}

func readKey(ctx *scenarios.Context) (string, error) {
	b, err := os.ReadFile(filepath.Join(ctx.PoCDir, Endpoint))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func tmmPods(ctx *scenarios.Context) []string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", floNS, "get", "pods", "-l", "app=f5-tmm",
		"--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	return strings.Fields(out)
}

// hasVirtualServer reports whether every TMM has the route's virtual server,
// named <ns>-<route>-<vip>-http-80-vs by f5ingress.
func hasVirtualServer(ctx *scenarios.Context) bool {
	pods := tmmPods(ctx)
	if len(pods) == 0 {
		return false
	}
	want := fmt.Sprintf("%s-%s-%s-http-80-vs", floNS, Route, VIP)
	for _, p := range pods {
		out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", floNS, "exec", p, "-c", "debug", "--",
			"tmctl", "-d", "blade", "-w", "200", "virtual_server_stat", "-s", "name")
		if !strings.Contains(out, want) {
			return false
		}
	}
	return true
}

func profileProgrammed(ctx *scenarios.Context) string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", floNS, "get", "aigatewayprofiles.inference.k8s.f5.com", Route,
		"-o", `jsonpath={.status.conditions[?(@.type=="Programmed")].status}: {.status.conditions[?(@.type=="Programmed")].message}`)
	return "AIGatewayProfile Programmed=" + strings.TrimSpace(out)
}

// ensureVirtualServer waits for TMM to have the route's virtual server. In
// f5ingress 2.4.1 a new route's Gateway apply can fail with "AI profile ... is
// not prepared yet" and is never retried: the profile then waits for the
// Gateway forever. Restarting f5-cne-controller re-applies every Gateway with
// the profile prepared; it is done at most once.
func ensureVirtualServer(ctx *scenarios.Context) (ok, restarted bool) {
	waitVS := func(d time.Duration) bool {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if hasVirtualServer(ctx) {
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
	if waitVS(90 * time.Second) {
		return true, false
	}
	fmt.Fprintf(ctx.Out, "      %s; restarting %s once (f5ingress 2.4.1 AI profile race)\n",
		profileProgrammed(ctx), controllerDeploy)
	r := ctx.Runner
	_ = r.Kubectl(ctx.Ctx, "-n", floNS, "rollout", "restart", "deploy/"+controllerDeploy)
	_ = r.Kubectl(ctx.Ctx, "-n", floNS, "rollout", "status", "deploy/"+controllerDeploy, "--timeout=5m")
	return waitVS(3 * time.Minute), true
}

func waitVIPAdvertised(ctx *scenarios.Context, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		scenarios.RetriggerRedistribute(ctx)
		if t, _ := scenarios.FRRVtysh(ctx, "show bgp ipv4 unicast"); strings.Contains(t, VIP+"/32") {
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

// statusMark prefixes curl's status line so it can be found in output that
// also carries the runtime's own messages (FRRNetnsRun merges stderr).
const statusMark = "OCIBNKCTL_HTTP_STATUS="

var statusLine = regexp.MustCompile(`(?m)^` + statusMark + `(\d+)\s*$`)

// chat sends one chat completion from the edge FRR's netns (outside the
// cluster, routed to the VIP over BGP) and returns the status and body.
func chat(ctx *scenarios.Context, key, model string) (int, string) {
	args := []string{"-s", "-o", "-", "-w", "\n" + statusMark + "%{http_code}\n",
		"-H", "Content-Type: application/json",
		"-d", fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello from ocibnkctl"}]}`, model)}
	if key != "" {
		args = append(args, "-H", "Authorization: Bearer "+key)
	}
	// Not FRRNetnsCurl: its --fail turns the 401/403 we assert on into errors.
	out, _ := scenarios.FRRNetnsRun(ctx, nil,
		append(append([]string{"curl", "--max-time", "15"}, args...), "http://"+VIP+"/v1/chat/completions")...)
	return parseChat(out)
}

// parseChat splits curl's output into the status and the response body: the
// line just before the status marker.
func parseChat(out string) (int, string) {
	m := statusLine.FindStringSubmatchIndex(out)
	if m == nil {
		return 0, strings.TrimSpace(out)
	}
	code, _ := strconv.Atoi(out[m[2]:m[3]])
	before := strings.TrimRight(out[:m[0]], "\n")
	if i := strings.LastIndex(before, "\n"); i >= 0 {
		before = before[i+1:]
	}
	return code, before
}

// meteredRecords checks ClickHouse for the tenant's records by status. The
// gateway writes record_version 2, whose values are in the OpenTelemetry
// gen_ai.* and http.response.status_code columns. clickhouse-client inside
// the pod authenticates from the CLICKHOUSE_USER/CLICKHOUSE_PASSWORD env FLO
// sets there, so no credential is read or passed here.
func meteredRecords(ctx *scenarios.Context) (bool, string) {
	q := "SELECT `http.response.status_code` AS s, count() AS n, sum(`gen_ai.f5.usage.total_tokens`) AS t " +
		"FROM otel.aigw_requests WHERE tenant = '" + Tenant + "' GROUP BY s ORDER BY s FORMAT TSV"
	var out string
	var err error
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		out, err = ctx.Runner.KubectlCapture(ctx.Ctx, "-n", floNS, "exec", clickHousePod, "--",
			"clickhouse-client", "-q", q)
		if err == nil && strings.Contains(out, "200\t") && strings.Contains(out, "401\t") && strings.Contains(out, "403\t") {
			break
		}
		select {
		case <-ctx.Ctx.Done():
			return false, "cancelled"
		case <-time.After(5 * time.Second):
		}
	}
	if err != nil {
		return false, oneLine(err.Error(), 200)
	}
	var parts []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 3 {
			parts = append(parts, fmt.Sprintf("%s×%s (%s tokens)", f[0], f[1], f[2]))
		}
	}
	got := strings.Join(parts, ", ")
	return strings.Contains(out, "200\t") && strings.Contains(out, "401\t") && strings.Contains(out, "403\t"), got
}

// tryIt is printed after a green run: how to open the console and send a
// request yourself. It names Secrets rather than printing their values.
func tryIt(ctx *scenarios.Context) string {
	keyPath := filepath.Join(ctx.PoCDir, Endpoint)
	kc := filepath.Join(ctx.PoCDir, "artifacts", "kubeconfig")
	frr := scenarios.EdgeFRRName(ctx)
	return fmt.Sprintf(`
  Try it yourself
  ---------------
  Console (tenant %[1]s, user admin):
    export KUBECONFIG=%[2]s
    kubectl get secret %[3]s -o jsonpath='{.data.password}' | base64 -d; echo
    kubectl port-forward svc/%[4]s 3000:3000      # then open http://localhost:3000
    # from a laptop: ssh -L 3000:127.0.0.1:3000 <this-host>, run the port-forward there

  A request through TMM, as a client outside the cluster (the edge FRR's netns):
    docker run --rm --network container:%[5]s curlimages/curl:8.10.1 -s \
      http://%[6]s/v1/chat/completions \
      -H "Authorization: Bearer $(cat %[7]s)" -H 'Content-Type: application/json' \
      -d '{"model":"%[8]s","messages":[{"role":"user","content":"hello"}]}'
`, Tenant, kc, fmt.Sprintf(tenantAdminPattern, Tenant), consoleService, frr, VIP, keyPath, Model)
}
