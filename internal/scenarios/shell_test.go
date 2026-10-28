package scenarios

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
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

// With the logging subsystem on, ai-token-counting skips the iRule-log
// check instead of failing it (TMM's lines are not in kubectl logs then).
func TestTokenCountingLoggingSubsystem(t *testing.T) {
	stubs(t, strings.Replace(healthyKubectl, `*"get cneinstances"*wholeCluster*)`,
		`*"get cneinstances"*loggingSubsystem*) printf true ;;
*"get cneinstances"*wholeCluster*)`, 1), healthyDocker)
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "ai-token-counting")
	last := r.Assertions[len(r.Assertions)-1]
	if r.Status != "ok" || !last.Skipped || !strings.Contains(last.Got, "loggingSubsystem") {
		t.Fatalf("status=%s last=%+v", r.Status, last)
	}
}

// The virtual key lands in <PoC>/artifacts/llm-gateway/virtual-key (0600,
// where ask.sh reads it) and never in the scenario's output.
func TestLLMGatewayKeyHandling(t *testing.T) {
	stubs(t, healthyKubectl, healthyDocker)
	t.Setenv("EDGE_DRIVER", "")
	t.Setenv("KIT_STATE", "")
	ctx := &Context{Ctx: context.Background(), PoC: poc.New("stub"), PoCDir: t.TempDir(), Out: &strings.Builder{}}
	s := Find("llm-gateway")
	if _, err := s.Manifests(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply(ctx); err != nil {
		t.Fatalf("apply: %v\n%s", err, ctx.Out)
	}
	if r := s.Verify(ctx); r.Status != "ok" {
		t.Fatalf("verify: %s %q", r.Status, r.Summary)
	}
	path := filepath.Join(ctx.PoCDir, "artifacts", "llm-gateway", "virtual-key")
	b, err := os.ReadFile(path)
	fi, _ := os.Stat(path)
	if err != nil || strings.TrimSpace(string(b)) != "vk-test-123" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %s: %q %v mode=%v", path, b, err, fi)
	}
	if strings.Contains(ctx.Out.(*strings.Builder).String(), "vk-test-123") {
		t.Error("the virtual key appears in the scenario output")
	}
}

// A gateway that serves a model outside the key's scope fails that check.
func TestLLMGatewayScopeEnforced(t *testing.T) {
	stubs(t, healthyKubectl, strings.Replace(healthyDocker,
		`*"Bearer vk-test-123"*gpt-5*) printf '{"error":"Model not allowed"}\nOCIBNKCTL_HTTP_STATUS=403\n' ;;`,
		`*"Bearer vk-test-123"*gpt-5*) printf '{"usage":{}}\nOCIBNKCTL_HTTP_STATUS=200\n' ;;`, 1))
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "llm-gateway")
	if r.Status != "failed" || r.Summary != "failed: model outside the key's scope → 403" {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
	}
}

// Half the requests on the busy pod is load-blind balancing: steering fails.
func TestEPPSteeringFails(t *testing.T) {
	stubs(t, healthyKubectl, strings.Replace(healthyDocker,
		`*'seq 10'*) for i in 1 2 3 4 5 6 7 8 9 10; do echo "ocibnkctl-epp-role=idle pod=mock-vllm-idle-1"; done ;;`,
		`*'seq 10'*) for i in 1 2 3 4 5; do echo ocibnkctl-epp-role=idle; echo ocibnkctl-epp-role=busy; done ;;`, 1))
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "ai-inference-epp")
	if r.Status != "failed" || !strings.Contains(r.Summary, "EPP steered every request to the idle pod") {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
	}
}

// One token off in one DSSM bucket fails the exact-match check, and only it.
func TestDSSMMismatch(t *testing.T) {
	stubs(t, strings.Replace(healthyKubectl, `echo "x:TMMTOKtotal|vs=$lb|mode=$m|model=gpt-stub~~~S75"`,
		`v=75; [ "$lb|$m" = "llm-rag|streaming" ] && v=74; echo "x:TMMTOKtotal|vs=$lb|mode=$m|model=gpt-stub~~~S$v"`, 1), healthyDocker)
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "ai-token-counting-dssm")
	if r.Status != "failed" || r.Summary != "failed: DSSM counters match backend usage exactly (per LB, per mode, incl. streaming)" {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
	}
}

// grpc-loadbalance takes grpcurl from the checksum-verified cache.
func TestGRPCUsesVerifiedGrpcurl(t *testing.T) {
	stubs(t, healthyKubectl, healthyDocker)
	t.Setenv("EDGE_DRIVER", "")
	r := runPilot(t, "grpc-loadbalance")
	for _, a := range r.Assertions {
		if a.Description == "grpcurl available on the edge client" && strings.Contains(a.Got, "SHA-256 verified") {
			return
		}
	}
	t.Fatalf("grpcurl not taken from the verified cache: %+v", r.Assertions)
}

// A session up for days shows Up/Down as 2d17h34m, not hh:mm:ss; it is
// still Established (the prefix count in State/PfxRcd says so).
func TestBGPPeerLongLivedSession(t *testing.T) {
	stubs(t, healthyKubectl, strings.Replace(healthyDocker, `0 0 0 00:02:13 3"`,
		`0 0 0 2d17h34m 8 8 N/A"`, 1))
	t.Setenv("EDGE_DRIVER", "")
	if r := runPilot(t, "bgp-peer-frr"); r.Status != "ok" {
		t.Fatalf("status=%s assertions=%+v", r.Status, r.Assertions)
	}
}

// With the in-cluster pod client every scenario's data-plane checks run
// (BGP-table checks skip: the pod has no router CLI), and the pod is routed
// via the TMM's address on the NAD.
func TestShellScenariosPodDriver(t *testing.T) {
	for _, s := range loadShellScenarios() {
		t.Run(s.Name(), func(t *testing.T) {
			stubs(t, healthyKubectl, healthyDocker)
			t.Setenv("EDGE_DRIVER", "pod")
			ctx := &Context{Ctx: context.Background(), PoC: poc.New("stub"), PoCDir: t.TempDir(), Out: &strings.Builder{}}
			if _, err := s.Manifests(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Apply(ctx); err != nil {
				t.Fatalf("apply: %v\n%s", err, ctx.Out)
			}
			r := s.Verify(ctx)
			if r.Status != "ok" {
				t.Fatalf("status=%s summary=%q\n%s", r.Status, r.Summary, ctx.Out)
			}
			for _, a := range r.Assertions {
				if a.Skipped && !strings.Contains(a.Got, "EDGE_VTYSH") && !strings.Contains(a.Got, "loggingSubsystem") {
					t.Errorf("%q skipped for another reason: %s", a.Description, a.Got)
				}
			}
		})
	}
}

// The pod client routes the VIP range via the TMM's net1 address, parsed
// from its Multus network-status, and is set up once per pod.
func TestEdgePodRoutesViaTMM(t *testing.T) {
	stubs(t, healthyKubectl, healthyDocker)
	t.Setenv("EDGE_DRIVER", "pod")
	if r := runPilot(t, "http-routing-e2e"); r.Status != "ok" {
		t.Fatalf("status=%s summary=%q", r.Status, r.Summary)
	}
	routes, err := os.ReadFile(os.Getenv("STUB_STATE") + ".route")
	if err != nil || strings.Count(string(routes), "ip route replace 203.0.113.0/24 via 192.168.99.160 dev net1") != 1 {
		t.Fatalf("route setup = %q (%v), want one route via the TMM's net1 address", routes, err)
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
	// grpc-loadbalance fetches grpcurl; serve it from the cache offline: a
	// stand-in tarball whose checksum the test pins via scenario.env.
	cache := filepath.Join(dir, "cache")
	tgz := filepath.Join(cache, "grpcurl-1.9.3", "grpcurl.tgz")
	if err := os.MkdirAll(filepath.Dir(tgz), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "grpcurl"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("tar", "-czf", tgz, "-C", dir, "grpcurl").CombinedOutput(); err != nil {
		t.Fatalf("tar: %v %s", err, out)
	}
	body, _ := os.ReadFile(tgz)
	sum := fmt.Sprintf("%x", sha256.Sum256(body))
	t.Setenv("KIT_CACHE", cache)
	// ai-inference-epp fetches the GAIE CRDs; seed a stand-in the same way.
	gaie := filepath.Join(cache, "gaie-v1.4.0", "manifests.yaml")
	if err := os.MkdirAll(filepath.Dir(gaie), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gaie, []byte("# stand-in GAIE CRDs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GAIE_SHA", fmt.Sprintf("%x", sha256.Sum256([]byte("# stand-in GAIE CRDs\n"))))
	t.Setenv("GAIE_URL", "http://127.0.0.1:9/unreachable") // a fetch would fail loudly
	t.Setenv("GRPCURL_SHA_X86_64", sum)
	t.Setenv("GRPCURL_SHA_ARM64", sum)
}

// healthyKubectl answers like a healthy anycast-bgp BNK cluster for every
// call the kit scenarios make.
const healthyKubectl = `case "$*" in
*"exec edge-client -- "*)
	# the pod edge client: run its command through the client stub
	for a; do shift; [ "$a" = -- ] && break; done
	exec docker run --rm stub-client "$@" ;;
*"get pod edge-client"*labels*) [ -f "$STUB_STATE.routed" ] && printf yes; exit 0 ;;
*"label pod edge-client"*) touch "$STUB_STATE.routed" ;;
*"get pod f5-tmm-abc"*network-status*)
	printf '[{\n "name": "k8s-pod-network",\n "ips": ["10.42.0.9"]\n},{\n "name": "default/bnk-bgp",\n "interface": "net1",\n "ips": [\n  "192.168.99.160"\n ]\n}]' ;;
*"get gatewayclass"*) printf True ;;
*"get pod -l app=ext-backend"*) printf 10.42.1.5 ;;
*"get pool.k8s.f5net.com"*) printf '10.42.1.5:80 ' ;;
*"get service/stub-modelcache"*) printf 10.43.0.9 ;;
*"get gateway/scn-semcache-gateway"*annotations*) printf 'semantic_cache=enabled,semantic_cache_ip_port=10.43.0.9:5050' ;;
*"get httproute"*sse-enabled*) printf true ;;
*"get httproute scn-epp-route"* | *"get inferencepool"*) printf 'Accepted=True ResolvedRefs=True ' ;;
*"get f5-epps"*) printf 'Accepted=True ResolvedRefs=True Programmed=True Available=True ' ;;
*"get service llm-pool-epp"*) printf '9002 9003 9090' ;;
*"get role,rolebinding"*) echo role.rbac.authorization.k8s.io/llm-pool-epp ;;
*"get cneinstance bnk-instance"*eppNamespaces*) printf '["inference"]' ;;
*"tmctl -d blade profile_ext_proc_stat"*) echo "name scn-epp-llm-pool-epp-ivs" ;;
*"tmctl -d blade inference_epp_stats"*)
	# the captured tmctl shape; 2 consults before the run, 12 after
	n=$(cat "$STUB_STATE.epp" 2>/dev/null || echo 0); echo $((n + 1)) >"$STUB_STATE.epp"
	c=2; [ "$n" -gt 0 ] && c=12
	printf 'name          schedule_calls schedule_successes schedule_skipped\n------------- -------------- ------------------ ----------------\ninference_epp             %s                 %s                0\n\nschedule_fallbacks immediate_responses\n------------------ -------------------\n                 0                   0\n' $c $c ;;
*"get --raw"*metrics*)
	n=$(cat "$STUB_STATE.metrics" 2>/dev/null || echo 0); echo $((n + 1)) >"$STUB_STATE.metrics"
	m=2; [ "$n" -gt 0 ] && m=12
	echo 'epp_tokenizer_guard_events_total{event="encode_failed"} 20'
	echo "f5_epp_prefix_index_lookups_total{instance=\"a\",pool=\"llm-pool\",result=\"miss\"} $m"
	echo 'f5_epp_prefix_index_lookups_total{instance="a",pool="llm-pool",result="no_query"} 21' ;;
*"get grpcroute"*) printf True ;;
*"get f5-big-cne-irule/aitok-counter"*) printf aitok-counter ;;
*"exec f5-dssm-db-0"*)
	# before = empty, after = what the curl stub's usage adds up to:
	# 5 requests per LB per mode, each prompt 10 + completion 5 = total 15
	n=$(cat "$STUB_STATE.dssm" 2>/dev/null || echo 0); echo $((n + 1)) >"$STUB_STATE.dssm"
	[ "$n" -eq 0 ] && exit 0
	for lb in llm-chat llm-code llm-embed llm-rag; do for m in streaming non-streaming; do
		echo "x:TMMTOKtotal|vs=$lb|mode=$m|model=gpt-stub~~~S75"
		echo "x:TMMTOKprompt|vs=$lb|mode=$m|model=gpt-stub~~~S50"
		echo "x:TMMTOKcompletion|vs=$lb|mode=$m|model=gpt-stub~~~S25"
	done; done ;;
*"get coremonds"*) printf f5-coremond ;;
*"get daemonset -l app=f5-coremond"*) printf 'f5-coremond 1' ;;
*"get daemonset/f5-tmm"*volumes*) printf 'kernel-cores,config,' ;;
*"get cneinstance "*Coremond*) printf True ;;
*"get cneinstances"*metadata.namespace*) printf default/bnk-instance ;;
*patch* | *rollout*) ;;
*"get gatewaysettings"*) printf True/True ;;
*"get gateway/scn-fic-gateway"*addresses*) printf 203.0.113.111 ;;
*"get gateway/scn-tokencount-gateway"*annotations*) printf 'token_counting=enabled,user_id_source=api_key' ;;
*"get httproute"* | *"get l4route"*) printf True ;;
*"get f5-big-cne-irule/pp-prepend"*) printf pp-prepend ;;
*"get netpolicy"*) printf scn-proxy-attach ;;
*"get cneinstances"*aiGateway.enabled*) printf true ;;
*"get cneinstances"*aiGateway.aiMetrics*) printf true ;;
*"get secret f5-aigateway-admin"*) printf cGFzcw== ;;
*" create secret "* | *" create configmap "*) echo "apiVersion: v1" ;;
*"get job llm-setup"*) printf 1/ ;;
*"logs job/llm-setup"*) printf 'creating tenant demo\nVKEY=vk-test-123\n' ;;
*"virtual_server_stat"*) echo "default-demo-llm-203.0.113.140-http-80-vs" ;;
*"get aigatewayprofiles"*) printf 'Programmed=True: ok' ;;
*"exec f5-aigateway-clickhouse-0"*) printf '200\t1\t30\n401\t2\t0\n403\t1\t0\n' ;;
*"get cneinstances"*TMM_MAPRES*) printf FALSE ;;
*"get cneinstances"*USE_GATEWAY_SETTINGS*) printf true ;;
*"get cneinstances"*networkAttachments*) printf bnk-bgp ;;
*"get cneinstances"*wholeCluster*) printf true ;;
*"get cneinstances"*) ;;
*"get deployment/f5-cne-controller"*) printf 1 ;;
*"get pod -l app=f5-tmm"*) echo f5-tmm-abc ;;
*"logs f5-tmm-abc"*)
	echo "TOKEN COUNTING IRULE INIT scn-tokencount-gateway-scn-tokencount-token-counting TOKEN(12) cumulative=34"
	echo "Rule scn-semcache-gateway-scn-semcache-ai <CLIENT_ACCEPTED>: Client initialized with modelcache_server=10.43.0.9:5050" ;;
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
*"vtysh -c show bgp ipv4 unicast 192.168."*) echo "Paths: (1 available, best #1, table default)" ;;
*"vtysh -c show ip bgp"* | *"vtysh -c show bgp ipv4 unicast"*)
	for o in 100 101 102 103 104 106 107 108 109 111 120 121 122 123 124 140; do echo "*> 203.0.113.$o/32 192.168.99.160"; done ;;
*"ip route show"*) echo "$7 via 192.168.99.160 dev eth1" ;;
*"ip route replace"*) echo "$*" >>"$STUB_STATE.route" ;;
*"ip route get"*) echo "203.0.113.100 via 192.168.99.160 dev net1 src 192.168.99.170" ;;
*socat*) echo ocibnkctl-scenario-udp-lb-OK ;;
*"Bearer vk-test-123"*gpt-5*) printf '{"error":"Model not allowed"}\nOCIBNKCTL_HTTP_STATUS=403\n' ;;
*"Bearer vk-test-123"*) printf '{"choices":[],"usage":{"total_tokens":30}}\nOCIBNKCTL_HTTP_STATUS=200\n' ;;
*"Bearer sk-not-a-virtual-key"*) printf '{"error":"Invalid virtual key"}\nOCIBNKCTL_HTTP_STATUS=401\n' ;;
*OCIBNKCTL_HTTP_STATUS*) printf '{"error":"missing Authorization header"}\nOCIBNKCTL_HTTP_STATUS=401\n' ;;
*'seq 10'*) for i in 1 2 3 4 5 6 7 8 9 10; do echo "ocibnkctl-epp-role=idle pod=mock-vllm-idle-1"; done ;;
*'seq 1)'*) echo "ocibnkctl-epp-role=idle pod=mock-vllm-idle-1" ;;
*/grpcurl*Index*) echo '{"description": "gRPC testing server"}' ;;
*/grpcurl*list*) echo grpcbin.GRPCBin ;;
*curl*)
	# alternate the TCP LB backends so both weights show up
	n=$(cat "$STUB_STATE" 2>/dev/null || echo 0); echo $((n + 1)) >"$STUB_STATE"
	b=A; [ $((n % 2)) -eq 1 ] && b=B
	echo "ocibnkctl-scenario-httproute-e2e-OK ocibnkctl-scenario-tokencount-OK ocibnkctl-scenario-fic-dynamic-ip-OK"
	echo "ocibnkctl-scenario-extres-pool-OK ocibnkctl-scenario-semcache-OK"
	echo '{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}'
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
