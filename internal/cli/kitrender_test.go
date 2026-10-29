package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// The platform manifests come from kit/platform/lib.sh's render_*
// functions. TestRenderGolden pins their bytes for five PoC shapes; these
// pin the properties behind them, so a deliberate template change that
// breaks one fails here with a reason rather than only a golden diff.

// kitOut runs a kit render function for a PoC built by mutate.
func kitOut(t *testing.T, mutate func(*poc.PoC), script string, extra ...string) string {
	t.Helper()
	p := poc.New("t")
	if mutate != nil {
		mutate(p)
	}
	k, err := newKitRenderer(t.TempDir(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer k.close()
	out, err := k.run(script, extra...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantAll(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s missing %q:\n%s", what, w, got)
		}
	}
}

// Under wholeCluster FLO runs TMM as a DaemonSet, one pod per labelled
// worker: no tmmReplicas, whatever the worker count.
func TestKitCNEWholeCluster(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		got := kitOut(t, func(p *poc.PoC) { p.Cluster.TMMNodes = n }, "render_cne")
		wantAll(t, "CNEInstance", got, "wholeCluster: true")
		if strings.Contains(got, "tmmReplicas:") {
			t.Errorf("nodes %d: tmmReplicas under wholeCluster:\n%s", n, got)
		}
	}
}

// The NAD + mapres + network-model dispatch per dataplane mode. selfip-dag
// drives TMM through an F5SPKVlan, which BNK 2.4 reconciles only in the
// legacy SPK model (USE_GATEWAY_SETTINGS false); anycast-bgp keeps net1's
// kernel IP for BGP (mapres FALSE). The legacy active/active bool is
// selfip-dag.
func TestKitCNEDataplaneMode(t *testing.T) {
	cases := []struct {
		name                 string
		bnk                  func(*poc.BNK)
		nad, mapres, gateway string
	}{
		{"default is anycast-bgp", func(*poc.BNK) {}, "bnk-bgp", `"FALSE"`, `"true"`},
		{"standby", func(b *poc.BNK) { b.TMMDataplaneMode = poc.DataplaneStandby }, "", `"TRUE"`, `"true"`},
		{"selfip-dag", func(b *poc.BNK) { b.TMMDataplaneMode = poc.DataplaneSelfIPDAG }, "f5-tmm-dag", `"TRUE"`, `"false"`},
		{"legacy bool", func(b *poc.BNK) { b.ActiveActive = true }, "f5-tmm-dag", `"TRUE"`, `"false"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kitOut(t, func(p *poc.PoC) { p.BNK.TMMDataplaneMode = ""; c.bnk(&p.BNK) }, "render_cne")
			if c.nad == "" && strings.Contains(got, "networkAttachments:") {
				t.Errorf("want no networkAttachments:\n%s", got)
			}
			if c.nad != "" {
				wantAll(t, "CNEInstance", got, "- "+c.nad)
			}
			env := func(name string) string {
				i := strings.Index(got, `"`+name+`"`)
				if i < 0 {
					t.Fatalf("%s not rendered:\n%s", name, got)
				}
				tail := got[i:]
				return tail[:min(len(tail), 120)]
			}
			if w := env("TMM_MAPRES_ADDL_VETHS_ON_DP"); !strings.Contains(w, "value: "+c.mapres) {
				t.Errorf("mapres: want %s in %q", c.mapres, w)
			}
			if w := env("USE_GATEWAY_SETTINGS"); !strings.Contains(w, "value: "+c.gateway) {
				t.Errorf("USE_GATEWAY_SETTINGS: want %s in %q", c.gateway, w)
			}
		})
	}
}

// A small host sheds TMM's metrics sidecar.
func TestKitCNEMetricSubsystem(t *testing.T) {
	for profile, want := range map[string]string{"": "enabled: true", poc.HostProfileStandard: "enabled: true", poc.HostProfileSmall: "enabled: false"} {
		got := kitOut(t, func(p *poc.PoC) { p.BNK.HostProfile = profile }, "render_cne")
		i := strings.Index(got, "metricSubsystem:")
		if i < 0 {
			t.Fatalf("profile %q: no metricSubsystem:\n%s", profile, got)
		}
		if w := got[i:min(len(got), i+80)]; !strings.Contains(w, want) {
			t.Errorf("profile %q: want %q in %q", profile, want, w)
		}
	}
}

// AI gateway metrics flow through the platform fluentd, so they turn the
// logging subsystem on; settings without enabled render nothing.
func TestKitCNEAIGateway(t *testing.T) {
	cases := []struct {
		name      string
		gw        poc.AIGateway
		want, not []string
	}{
		{"off", poc.AIGateway{}, []string{"loggingSubsystem:\n      enabled: false"}, []string{"aiGateway:"}},
		{"console only", poc.AIGateway{Enabled: true},
			[]string{"aiGateway:\n    enabled: true\n    aiMetrics: false\n", "loggingSubsystem:\n      enabled: false"}, []string{"consoleURL:"}},
		{"metrics + console", poc.AIGateway{Enabled: true, AIMetrics: true, ConsoleURL: "http://192.168.99.10:30300"},
			[]string{"aiMetrics: true", `consoleURL: "http://192.168.99.10:30300"`, "loggingSubsystem:\n      enabled: true"}, nil},
		{"metrics without enabled", poc.AIGateway{AIMetrics: true},
			[]string{"loggingSubsystem:\n      enabled: false"}, []string{"aiGateway:"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kitOut(t, func(p *poc.PoC) { p.BNK.AIGateway = c.gw }, "render_cne")
			wantAll(t, "CNEInstance", got, c.want...)
			for _, n := range c.not {
				if strings.Contains(got, n) {
					t.Errorf("must not contain %q:\n%s", n, got)
				}
			}
		})
	}
}

// cne/install.sh reads the shrink policy's requests back at
// rules[shrink-f5-requests].mutate.foreach[0].patchStrategicMerge.spec.
// containers[0].resources.requests to size Multus; keep that shape. The
// policy caps requests only, never limits, and leaves f5-tmm alone.
func TestKitShrinkPolicy(t *testing.T) {
	got := kitOut(t, nil, "render_shrink_policy", "SHRINK_CPU=33m", "SHRINK_MEMORY=77Mi")
	wantAll(t, "shrink policy", got, "kind: ClusterPolicy", "app: f5-tmm", "{{ element.name }}",
		"namespaces: [f5-cne-core, f5-operators]")
	if strings.Contains(got, "limits:") {
		t.Errorf("shrink policy sets limits:\n%s", got)
	}
	var policy struct {
		Spec struct {
			Rules []struct {
				Name   string
				Mutate struct {
					Foreach []struct {
						PatchStrategicMerge struct {
							Spec struct {
								Containers []struct {
									Resources struct{ Requests map[string]string }
								}
							}
						} `yaml:"patchStrategicMerge"`
					}
				}
			}
		}
	}
	if err := yaml.Unmarshal([]byte(got), &policy); err != nil {
		t.Fatal(err)
	}
	for _, r := range policy.Spec.Rules {
		if r.Name != "shrink-f5-requests" {
			continue
		}
		if len(r.Mutate.Foreach) == 0 || len(r.Mutate.Foreach[0].PatchStrategicMerge.Spec.Containers) == 0 {
			t.Fatal("shrink-f5-requests has no foreach[0] container patch")
		}
		if req := r.Mutate.Foreach[0].PatchStrategicMerge.Spec.Containers[0].Resources.Requests; req["cpu"] != "33m" || req["memory"] != "77Mi" {
			t.Errorf("requests = %v, want cpu=33m memory=77Mi", req)
		}
		return
	}
	t.Fatal("no shrink-f5-requests rule")
}

// The Multus fixes: the shim installs by rename (a plain cp hits "Text file
// busy" while the old shim runs), and resources get a 500Mi limit.
func TestKitMultusPatches(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(kitOut(t, nil, "multus_patches 25m 128Mi")), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 patches, got %d", len(lines))
	}
	var shim []struct {
		Op, Path string
		Value    []string
	}
	if err := json.Unmarshal([]byte(lines[0]), &shim); err != nil || len(shim) != 1 {
		t.Fatalf("shim patch %q: %v", lines[0], err)
	}
	if shim[0].Path != "/spec/template/spec/initContainers/0/command" || len(shim[0].Value) != 3 ||
		!strings.Contains(shim[0].Value[2], "multus-shim.tmp && mv -f") {
		t.Errorf("shim patch = %+v", shim[0])
	}
	var res []struct{ Op, Path, Value string }
	if err := json.Unmarshal([]byte(lines[1]), &res); err != nil {
		t.Fatalf("resources patch %q: %v", lines[1], err)
	}
	got := map[string]string{}
	for _, o := range res {
		got[o.Path] = o.Value
	}
	const base = "/spec/template/spec/containers/0/resources/"
	if got[base+"limits/memory"] != "500Mi" || got[base+"requests/cpu"] != "25m" || got[base+"requests/memory"] != "128Mi" || len(got) != 3 {
		t.Errorf("resources patch = %v", got)
	}
}

// One self-IP per TMM from .10, below the NAD's IPAM range (.20) even at
// validate's selfip-dag worker cap.
func TestKitDAGSelfIPs(t *testing.T) {
	for n, want := range map[int]string{0: "192.0.2.10", 1: "192.0.2.10", 3: "192.0.2.10,192.0.2.11,192.0.2.12"} {
		if got := strings.TrimSpace(kitOut(t, func(p *poc.PoC) { p.Cluster.TMMNodes = n }, "dag_self_ips")); got != want {
			t.Errorf("%d workers: %q, want %q", n, got, want)
		}
	}
	ips := strings.Split(strings.TrimSpace(kitOut(t, func(p *poc.PoC) { p.Cluster.TMMNodes = poc.MaxSelfIPDAGWorkers }, "dag_self_ips")), ",")
	if last := ips[len(ips)-1]; last >= "192.0.2.20" || len(last) != len("192.0.2.10") {
		t.Errorf("last self-IP at %d workers = %s, must stay below the IPAM start 192.0.2.20", poc.MaxSelfIPDAGWorkers, last)
	}
	got := kitOut(t, func(p *poc.PoC) { p.Cluster.TMMNodes = 2 }, "render_f5spkvlan")
	wantAll(t, "F5SPKVlan", got, "kind: F5SPKVlan", "selfip_v4s: [192.0.2.10,192.0.2.11]", "pod_hash: SRC_ADDR")
}

// router-id must stay FLO's per-pod token (distinct sessions per TMM);
// VIPs are redistributed, not listed.
func TestKitOcNOSTemplate(t *testing.T) {
	got := kitOut(t, nil, "render_ocnos")
	wantAll(t, "OcNOS template", got, "router bgp 65000", "bgp router-id %%POD_IP%%",
		"redistribute kernel", "redistribute connected",
		"neighbor 192.168.99.41 remote-as 65001", "neighbor 192.168.99.41 update-source net1")
	if strings.Contains(got, "network ") {
		t.Errorf("no VIP: want no network statement:\n%s", got)
	}
}

// `imish -f` starts in config mode: no `configure terminal`, two exits.
func TestKitOcNOSRedistribute(t *testing.T) {
	got := kitOut(t, nil, `LOCAL_AS=65000 sh "$KIT_LIB/render.sh" "$KIT_LIB/ocnos-redistribute.cfg"`)
	if strings.Contains(got, "configure terminal") {
		t.Error("redistribute opens with `configure terminal`, which imish -f rejects")
	}
	wantAll(t, "redistribute", got, "router bgp 65000", "address-family ipv4 unicast",
		"redistribute kernel route-map RMALL", "redistribute connected route-map RMALL")
	if n := strings.Count(got, "\nexit"); n != 2 {
		t.Errorf("%d exits, want 2", n)
	}
}

// The IPAM slice stays clear of the static VIPs the scenarios pin.
func TestKitInfra(t *testing.T) {
	got := kitOut(t, nil, "render_infra")
	wantAll(t, "Infra", got, "kind: Infra", "name: bnk-dynamic-vips", "rangeStart: 203.0.113.110", "rangeEnd: 203.0.113.119")
	for _, static := range []string{"203.0.113.100", "203.0.113.109", "203.0.113.120"} {
		if strings.Contains(got, static) {
			t.Errorf("Infra pool includes static VIP %s", static)
		}
	}
}

func TestKitLicense(t *testing.T) {
	plain := kitOut(t, nil, "render_license", "JWT=abc")
	wantAll(t, "License", plain, `jwt: "abc"`, `operationMode: "connected"`, "namespace: f5-cne-core")
	if strings.Contains(plain, "jwksConfigMapRef") {
		t.Errorf("no JWKS ConfigMap: want no jwksConfigMapRef:\n%s", plain)
	}
	tst := kitOut(t, nil, "render_license cwc-jwks-cm", "JWT=abc")
	wantAll(t, "License", tst, "jwksConfigMapRef:\n    name: cwc-jwks-cm\n    key: key")

	var cm struct {
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	if err := yaml.Unmarshal([]byte(kitOut(t, nil, "render_jwks_configmap")), &cm); err != nil {
		t.Fatal(err)
	}
	var jwks struct {
		Keys []struct {
			Kid, Alg string
			X5c      []string
		}
	}
	if cm.Metadata.Name != "cwc-jwks-cm" || cm.Metadata.Namespace != "f5-cne-core" ||
		json.Unmarshal([]byte(cm.Data["key"]), &jwks) != nil || len(jwks.Keys) != 1 || jwks.Keys[0].Kid != "v1" || len(jwks.Keys[0].X5c) != 4 {
		t.Errorf("JWKS ConfigMap = %+v / %+v", cm.Metadata, jwks)
	}
}

// jwt_type classifies by the jku host, then a TST- subject, then a
// synthetic tst claim. iss and kid are the same in both and never count.
func TestKitJWTType(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	cases := []struct{ name, header, claims, want string }{
		{"prod by jku", `{"kid":"v1","jku":"https://product.apis.f5.com/ee/v1/keys/jwks"}`, `{"iss":"F5 Inc.","sub":"CUSTOMER-X"}`, "prod"},
		{"tst by jku", `{"kid":"v1","jku":"https://product-tst.apis.f5networks.net/ee/v1/keys/jwks"}`, `{"iss":"F5 Inc.","sub":"TST-EE4C16F4"}`, "tst"},
		{"tst by sub", `{"alg":"RS256"}`, `{"sub":"TST-1234"}`, "tst"},
		{"tst by claim", `{"alg":"RS256"}`, `{"iss":"F5 Inc.","tst":true}`, "tst"},
		{"iss and kid are not signals", `{"alg":"RS512","kid":"v1"}`, `{"iss":"F5 Inc.","sub":"PROD-1234"}`, "prod"},
	}
	for _, c := range cases {
		f := filepath.Join(t.TempDir(), "jwt")
		if err := os.WriteFile(f, []byte(enc(c.header)+"."+enc(c.claims)+".sig\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(kitOut(t, nil, "jwt_type", "JWT_FILE="+f)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// examples/license-tst-jwt.yaml is what `deploy cne` applies for a test JWT,
// as a plain manifest for FLO clusters ocibnkctl did not deploy.
func TestExampleLicenseTstJWT(t *testing.T) {
	raw, err := os.ReadFile("../../examples/license-tst-jwt.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want := kitOut(t, nil, "render_jwks_configmap; echo ---; render_license cwc-jwks-cm", "JWT=<JWT>")
	norm := func(s string) []any {
		var docs []any
		dec := yaml.NewDecoder(strings.NewReader(s))
		for {
			var d any
			if dec.Decode(&d) != nil {
				return docs
			}
			docs = append(docs, d)
		}
	}
	g, _ := json.Marshal(norm(string(raw)))
	w, _ := json.Marshal(norm(want))
	if string(g) != string(w) {
		t.Errorf("examples/license-tst-jwt.yaml differs from the kit's render:\n got %s\nwant %s", g, w)
	}
}
