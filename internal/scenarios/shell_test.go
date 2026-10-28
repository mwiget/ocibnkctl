package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

func TestShellScenariosLoad(t *testing.T) {
	for _, name := range []string{"cwc-admin-access", "cluster-wide-watch", "http-routing-e2e"} {
		s, ok := Find(name).(*shellScenario)
		if !ok {
			t.Fatalf("%s: not registered as a kit (shell) scenario", name)
		}
		// An unquoted " #" in YAML starts a comment and silently cuts the
		// title; every pilot title names its how-to number.
		if !strings.Contains(s.Title(), "how-to #") || s.Rating() != Green || s.Description() == "" {
			t.Errorf("%s: incomplete metadata: title=%q rating=%q", name, s.Title(), s.Rating())
		}
	}
}

func TestParseVerify(t *testing.T) {
	var log strings.Builder
	res := parseVerify(strings.NewReader(`not json
{"description":"a","ok":true,"got":"x"}
{"description":"b","ok":false}
{"description":"c","ok":true,"skipped":true,"got":"why"}
{"summary":"done"}
{"other":1}
`), &log)
	if len(res.Assertions) != 3 || res.Summary != "done" {
		t.Fatalf("got %+v", res)
	}
	if !res.Assertions[2].Skipped || res.Assertions[1].OK {
		t.Errorf("assertion fields wrong: %+v", res.Assertions)
	}
	if log.String() != "not json\n{\"other\":1}\n" {
		t.Errorf("passthrough log = %q", log.String())
	}
	if f := finalizeShell(res); f.Status != "failed" || f.Summary != "failed: b" {
		t.Errorf("finalize = %s %q", f.Status, f.Summary)
	}
}

// stubs writes fake kubectl/docker commands into a directory prepended to
// PATH. Each prints canned answers for the calls the pilots make.
func stubs(t *testing.T, kubectl, docker string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"kubectl": kubectl, "docker": docker} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A healthy cluster for the http-routing-e2e and cluster-wide-watch checks.
const healthyKubectl = `case "$*" in
*"get gatewayclass"*) printf True ;;
*"get httproute"*) printf True ;;
*"get cneinstances"*wholeCluster*) printf true ;;
*"get cneinstances"*) ;;
*"get deployment/f5-cne-controller"*) printf 1 ;;
*"get pod -l app=f5-tmm"*) echo f5-tmm-abc ;;
*"exec -i"*) cat >/dev/null ;;
*wait*) echo "condition met" ;;
esac
exit 0
`

// The external FRR: has the VIP in BGP, a kernel route, and curls succeed.
const healthyDocker = `case "$*" in
*vtysh*) echo "*> 203.0.113.100/32 192.168.99.160" ;;
*"ip route show"*) echo "203.0.113.100 via 192.168.99.160 dev eth1" ;;
*curl*) echo ocibnkctl-scenario-httproute-e2e-OK ;;
esac
`

func runPilot(t *testing.T, name string) Result {
	t.Helper()
	ctx := &Context{
		Ctx:    context.Background(),
		PoC:    poc.New("stub"),
		PoCDir: t.TempDir(),
		Out:    &strings.Builder{},
	}
	s := Find(name)
	if _, err := s.Manifests(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx); err != nil {
		t.Fatalf("apply: %v\n%s", err, ctx.Out)
	}
	r := s.Verify(ctx)
	if t.Failed() || r.Status != "ok" {
		t.Logf("log:\n%s", ctx.Out)
	}
	return r
}

func TestPilotHTTPRoutingEdgeNone(t *testing.T) {
	stubs(t, healthyKubectl, "exit 1\n")
	t.Setenv("EDGE_DRIVER", "none")
	r := runPilot(t, "http-routing-e2e")
	skipped := 0
	for _, a := range r.Assertions {
		if a.Skipped {
			skipped++
		}
	}
	if r.Status != "ok" || len(r.Assertions) != 6 || skipped != 3 || !strings.Contains(r.Summary, "3 check(s) skipped") {
		t.Fatalf("status=%s summary=%q assertions=%+v", r.Status, r.Summary, r.Assertions)
	}
}

// With the ocibnkctl edge driver (the adapter's default for a PoC), every
// check runs against the FRR container through the runtime CLI.
func TestPilotHTTPRoutingEdgeOcibnkctl(t *testing.T) {
	stubs(t, healthyKubectl, healthyDocker)
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "http-routing-e2e")
	if r.Status != "ok" || len(r.Assertions) != 6 {
		t.Fatalf("status=%s summary=%q assertions=%+v", r.Status, r.Summary, r.Assertions)
	}
	for _, a := range r.Assertions {
		if a.Skipped {
			t.Errorf("%s skipped with an edge client present", a.Description)
		}
	}
}

func TestPilotHTTPRoutingFailure(t *testing.T) {
	stubs(t, strings.Replace(healthyKubectl, `*wait*) echo "condition met" ;;`,
		`*"gateway/scn-gateway"*) echo "timed out"; exit 1 ;;
*wait*) echo "condition met" ;;`, 1), "exit 1\n")
	t.Setenv("EDGE_DRIVER", "none")
	r := runPilot(t, "http-routing-e2e")
	if r.Status != "failed" || r.Summary != "failed: Gateway Programmed=True" {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
	}
}

func TestPilotClusterWideWatch(t *testing.T) {
	stubs(t, healthyKubectl, "exit 1\n")
	t.Setenv("EDGE_DRIVER", "none")
	if r := runPilot(t, "cluster-wide-watch"); r.Status != "ok" || len(r.Assertions) != 5 {
		t.Fatalf("status=%s summary=%q assertions=%+v", r.Status, r.Summary, r.Assertions)
	}
}

// The CWC stub answers like the real gate: 200 + license JSON only with
// the in-pod token expression, 401 otherwise.
func TestPilotCWCAdminAccess(t *testing.T) {
	stubs(t, `case "$*" in
*"get secret"*go-template*) printf '  token: dG9rZW4=\n' ;;
*"get pod -l app=probe"*) printf probe-1 ;;
*exec*not-the-real-token*) printf '{"error":"unauthorized"}\n__HTTP_CODE__=401' ;;
*exec*'$(cat /token/token)'*) printf '{"LicenseStatus":"Active","DigitalAssetID":"x"}\n__HTTP_CODE__=200' ;;
*exec*) printf '__HTTP_CODE__=401' ;;
*apply*) cat >/dev/null ;;
*wait*) echo "condition met" ;;
esac
exit 0
`, "exit 1\n")
	t.Setenv("EDGE_DRIVER", "none")
	r := runPilot(t, "cwc-admin-access")
	if r.Status != "ok" || len(r.Assertions) != 4 {
		t.Fatalf("status=%s summary=%q assertions=%+v", r.Status, r.Summary, r.Assertions)
	}
}

// A failed preflight stops apply.sh with the reason.
func TestPilotPreflightFails(t *testing.T) {
	stubs(t, `case "$*" in *"get crd"*) exit 1 ;; esac
exit 0
`, "exit 1\n")
	t.Setenv("EDGE_DRIVER", "none")
	ctx := &Context{Ctx: context.Background(), PoC: poc.New("stub"), PoCDir: t.TempDir(), Out: &strings.Builder{}}
	s := Find("http-routing-e2e")
	if _, err := s.Manifests(ctx); err != nil {
		t.Fatal(err)
	}
	err := s.Apply(ctx)
	if err == nil || !strings.Contains(ctx.Out.(*strings.Builder).String(), "CRD gateways.gateway.networking.k8s.io not installed") {
		t.Fatalf("err=%v log:\n%s", err, ctx.Out)
	}
}
