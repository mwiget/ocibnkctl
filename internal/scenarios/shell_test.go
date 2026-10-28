package scenarios

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// Every kit script scenario runs its real apply.sh + verify.sh end to end
// against a healthy stub cluster: all checks pass with the ocibnkctl edge,
// and with no edge client the data-plane ones are skipped, not failed.
func TestShellScenariosHealthy(t *testing.T) {
	for _, s := range loadShellScenarios() {
		for _, driver := range []string{"", "none"} {
			t.Run(s.Name()+"/edge="+driver, func(t *testing.T) {
				stubs(t, healthyKubectl, healthyDocker)
				t.Setenv("EDGE_DRIVER", driver)
				r := runPilot(t, s.Name())
				if r.Status != "ok" {
					t.Fatalf("status=%s summary=%q assertions=%+v", r.Status, r.Summary, r.Assertions)
				}
				for _, a := range r.Assertions {
					if driver == "" && a.Skipped {
						t.Errorf("%q skipped with an edge client present", a.Description)
					}
				}
			})
		}
	}
}

// bgp-peer-frr counts only Established rows on the edge L2: an Active
// neighbor next to an Established one is 1 of 2, a failure.
func TestBGPPeerCountsEstablished(t *testing.T) {
	stubs(t, strings.Replace(healthyKubectl, `echo f5-tmm-abc`, `printf 'f5-tmm-a\nf5-tmm-b\n'`, 1),
		strings.Replace(healthyDocker, `echo "*192.168.99.160 4 65000 12 14 0 0 0 00:02:13 3" ;;`,
			`echo "*192.168.99.160 4 65000 12 14 0 0 0 00:02:13 3"; echo "*192.168.99.161 4 65000 0 0 0 0 0 never Active" ;;`, 1))
	t.Setenv("EDGE_DRIVER", "")
	t.Setenv("BGP_WAIT", "1")
	r := runPilot(t, "bgp-peer-frr")
	if r.Status != "failed" || !strings.Contains(r.Assertions[0].Got, "1/2 established") {
		t.Fatalf("status=%s assertions=%+v", r.Status, r.Assertions)
	}
}

func TestShellScenariosLoad(t *testing.T) {
	for _, s := range loadShellScenarios() {
		if _, err := os.Stat(filepath.Join("..", "..", "kit", s.KitDir(), "README.md")); err != nil {
			t.Errorf("%s: no README.md (render bundles link to it)", s.Name())
		}
	}
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
	t.Setenv("STUB_STATE", filepath.Join(dir, "state"))
	t.Setenv("REDISTRIBUTE_PAUSE", "0") // bgp-peer-frr's pacing between rounds
}

// healthyKubectl answers like a healthy anycast-bgp BNK cluster for every
// call the kit scenarios make.
const healthyKubectl = `case "$*" in
*"get gatewayclass"*) printf True ;;
*"get gatewaysettings"*) printf True/True ;;
*"get gateway/scn-fic-gateway"*addresses*) printf 203.0.113.111 ;;
*"get gateway/scn-tokencount-gateway"*annotations*) printf 'token_counting=enabled,user_id_source=api_key' ;;
*"get httproute"* | *"get l4route"*) printf True ;;
*"get f5-big-cne-irule/pp-prepend"*) printf pp-prepend ;;
*"get netpolicy"*) printf scn-proxy-attach ;;
*"get cneinstances"*TMM_MAPRES*) printf FALSE ;;
*"get cneinstances"*USE_GATEWAY_SETTINGS*) printf true ;;
*"get cneinstances"*networkAttachments*) printf bnk-bgp ;;
*"get cneinstances"*wholeCluster*) printf true ;;
*"get cneinstances"*) ;;
*"get deployment/f5-cne-controller"*) printf 1 ;;
*"get pod -l app=f5-tmm"*) echo f5-tmm-abc ;;
*"logs f5-tmm-abc"*) echo "TOKEN COUNTING IRULE INIT scn-tokencount-gateway-scn-tokencount-token-counting TOKEN(12) cumulative=34" ;;
*"exec -i"*) cat >/dev/null ;;
*"get secret"*go-template*) printf '  token: dG9rZW4=\n' ;;
*"get pod -l app=probe"*) printf probe-1 ;;
*exec*not-the-real-token*) printf '{"error":"unauthorized"}\n__HTTP_CODE__=401' ;;
*exec*'$(cat /token/token)'*) printf '{"LicenseStatus":"Active","DigitalAssetID":"x"}\n__HTTP_CODE__=200' ;;
*exec*) printf '__HTTP_CODE__=401' ;;
*apply*) cat >/dev/null ;;
*"get crd"* | *"get secret"*) ;;
*wait*) echo "condition met" ;;
esac
exit 0
`

// healthyDocker is the external FRR and the netshoot client in its netns:
// every scenario VIP in BGP, one Established TMM, routes and replies.
const healthyDocker = `case "$*" in
*"vtysh -c show ip bgp summary"*)
	echo "*192.168.99.160 4 65000 12 14 0 0 0 00:02:13 3" ;;
*"vtysh -c show ip bgp"* | *"vtysh -c show bgp ipv4 unicast"*)
	for o in 100 102 103 106 107 111; do echo "*> 203.0.113.$o/32 192.168.99.160"; done ;;
*"ip route show"*) echo "$7 via 192.168.99.160 dev eth1" ;;
*socat*) echo ocibnkctl-scenario-udp-lb-OK ;;
*curl*)
	# alternate the TCP LB backends so both weights show up
	n=$(cat "$STUB_STATE" 2>/dev/null || echo 0); echo $((n + 1)) >"$STUB_STATE"
	b=A; [ $((n % 2)) -eq 1 ] && b=B
	echo "ocibnkctl-scenario-httproute-e2e-OK ocibnkctl-scenario-tokencount-OK ocibnkctl-scenario-fic-dynamic-ip-OK"
	echo "ocibnkctl-scenario-proxy-protocol-OK proxy_addr=192.168.99.41 backend=$b" ;;
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

// A route via the wrong next hop fails the kernel-route check.
func TestPilotHTTPRoutingWrongNexthop(t *testing.T) {
	stubs(t, healthyKubectl, strings.Replace(healthyDocker, "via 192.168.99.160", "via 10.0.0.1", 1))
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "http-routing-e2e")
	if r.Status != "failed" || !strings.Contains(r.Summary, "kernel route") {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
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
