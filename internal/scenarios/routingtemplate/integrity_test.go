package routingtemplate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/deploy"
)

// A real context dump read off a TMM on BNK 2.4.0 GA (f5ingress
// v14.91.12-0.4.7) with the legacy SPK model active
// (USE_GATEWAY_SETTINGS=false) and two F5SPKVlans programmed. This is the
// feature working: .vlans keyed by F5SPKVlanSpec.name, each carrying the six
// documented fields, selfIPv4 being THIS TMM's own address.
const ctxWithVlans = `! OCIBNKCTL-CTX map[global:map[spec:map[bgp:map[neighbors:[] routerIdSource:192.168.27.225 startASN:65000] templateRef:map[name:scn-rt-vlans]] tmmAllocatedIPs:map[DP-0:192.168.27.225] virtualIpTmmAssignment:map[]] routeDomainID:0 secrets:map[neighborPasswords:map[]] vlans:map[tenant-green:map[prefixLenV4:24 prefixLenV6:112 selfIPv4:198.51.100.21 selfIPv6:2001:db8:1::21 tag:2002 vrf:green] tenant-red:map[prefixLenV4:24 prefixLenV6:112 selfIPv4:198.51.100.11 selfIPv6:2001:db8:1::11 tag:2001 vrf:red]]]`

// The same cluster BEFORE the fix — Infra/GatewaySettings model, where the
// F5SPKVlan CRs are accepted and never reconciled, so the key is absent.
// Verify must report that as the cause, not as six mysterious field failures.
const ctxNoVlans = `! OCIBNKCTL-CTX map[global:map[spec:map[bgp:map[neighbors:[map[ip:192.168.100.41 remoteAs:65001]] routerIdSource:192.168.27.247 startASN:65000] templateRef:map[name:scn-rt-vlans]] tmmAllocatedIPs:map[DP-0:192.168.27.247] virtualIpTmmAssignment:map[]] routeDomainID:0 secrets:map[neighborPasswords:map[]]]`

func TestContextKeys(t *testing.T) {
	got := contextKeys(ctxWithVlans)
	for _, want := range []string{"global", "routeDomainID", "secrets", "vlans"} {
		if !hasKey(got, want) {
			t.Errorf("contextKeys = %v, want it to include %q", got, want)
		}
	}
	// Nested keys must not surface as top-level: reporting selfIPv4 or a VLAN
	// name here would make the .vlans gate pass on a build that merely
	// mentions the string.
	for _, unwanted := range []string{"selfIPv4", "tenant-red", "map"} {
		if hasKey(got, unwanted) {
			t.Errorf("contextKeys leaked nested key %q: %v", unwanted, got)
		}
	}

	got = contextKeys(ctxNoVlans)
	want := []string{"global", "routeDomainID", "secrets"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("contextKeys(no-vlans) = %v, want %v", got, want)
	}
	if hasKey(got, "vlans") {
		t.Error("no-vlans dump reported as having .vlans — the scenario would " +
			"claim the feature works while nothing is programmed")
	}

	if got := contextKeys("no dump here"); got != nil {
		t.Errorf("contextKeys(garbage) = %v, want nil", got)
	}
}

// The marker line is how per-TMM field values are read back; a parser slip
// would silently pass every field assertion with empty values.
func TestParseVlanMarkers(t *testing.T) {
	body := `route-map RMALL permit 10
!
` + vlanMarker + `tenant-red selfIPv4=198.51.100.11 selfIPv6=2001:db8:1::11 prefixLenV4=24 prefixLenV6=112 tag=2001 vrf=red
` + vlanMarker + `tenant-green selfIPv4=198.51.100.21 selfIPv6=2001:db8:1::21 prefixLenV4=24 prefixLenV6=112 tag=2002 vrf=green
!
router bgp 65000
`
	got := parseVlanMarkers(body)
	if len(got) != 2 {
		t.Fatalf("parsed %d vlans, want 2: %v", len(got), got)
	}
	red := got[vlanRed]
	for k, want := range map[string]string{
		"selfIPv4": "198.51.100.11", "selfIPv6": "2001:db8:1::11",
		"prefixLenV4": "24", "prefixLenV6": "112", "tag": "2001", "vrf": "red",
	} {
		if red[k] != want {
			t.Errorf("tenant-red[%s] = %q, want %q", k, red[k], want)
		}
	}
	if got[vlanGreen]["vrf"] != "green" || got[vlanGreen]["tag"] != "2002" {
		t.Errorf("tenant-green parsed wrong: %v", got[vlanGreen])
	}

	// Without .vlans the template's `{{- with $v := index $.vlans $name }}`
	// block is skipped entirely, so no marker line is emitted at all — the
	// tenant comes back absent, and every field assertion then compares ""
	// against the expected address and fails. That is the intended behaviour:
	// a missing feature must not silently pass.
	empty := parseVlanMarkers("route-map RMALL permit 10\n!\nrouter bgp 65000\n")
	if len(empty) != 0 {
		t.Errorf("a render with no marker lines parsed as %v, want empty", empty)
	}
	if v := empty[vlanRed]["selfIPv4"]; v != "" {
		t.Errorf("absent tenant yielded %q, want empty so the assertion fails", v)
	}

	// Junk without '=' must be dropped rather than becoming a bogus key.
	junk := parseVlanMarkers(vlanMarker + "tenant-red selfIPv4=1.2.3.4 garbage")
	if _, ok := junk[vlanRed]["garbage"]; ok {
		t.Error("non key=value token became a field")
	}
	if junk[vlanRed]["selfIPv4"] != "1.2.3.4" {
		t.Errorf("valid field lost alongside junk: %v", junk[vlanRed])
	}
}

// Per-TMM addressing is the property the whole design rests on: selfip_v4s
// element order maps to DP-0, DP-1, … so device i must get index i.
func TestPerDeviceSelfIPs(t *testing.T) {
	if redV4(0) == redV4(1) {
		t.Error("redV4 returns the same address for DP-0 and DP-1")
	}
	if redV4(0) != "198.51.100.11" || greenV4(0) != "198.51.101.11" {
		t.Errorf("unexpected DP-0 addresses: red=%s green=%s", redV4(0), greenV4(0))
	}
	// Separate subnets per tenant: with both inside one /24 the next-hop probe
	// would be ambiguous, since a reply could not be attributed to a VLAN.
	if strings.HasPrefix(greenV4(0), redSubnet+".") {
		t.Errorf("tenants share subnet %s — the next-hop probe cannot attribute a reply", redSubnet)
	}
	// The two tenants must never collide, or "isolation" is untestable.
	for i := 0; i < 8; i++ {
		if redV4(i) == greenV4(i) || redV6(i) == greenV6(i) {
			t.Fatalf("tenant address collision at index %d", i)
		}
	}
}

func TestRenderVlans(t *testing.T) {
	body := renderVlans(2)

	for _, want := range []string{
		"kind: F5SPKVlan",
		"name: " + vlanRed,
		"name: " + vlanGreen,
		"tag: 2001",
		"tag: 2002",
		"vrf: red",
		"vrf: green",
		`"198.51.100.11"`, `"198.51.100.12"`, // red: DP-0, DP-1
		`"198.51.101.11"`, `"198.51.101.12"`, // green: its OWN subnet
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered VLANs missing %q", want)
		}
	}
	// Both tenants must share the interface — two tags on one link is the
	// multi-tenant shape under test.
	if n := strings.Count(body, `interfaces: ["`+vlanInterface+`"]`); n != 2 {
		t.Errorf("both VLANs must bind %s, got %d", vlanInterface, n)
	}
	// One self-IP per TMM per tenant, or TMMs beyond the first render empty.
	if n := strings.Count(body, redSubnet+"."); n != 2 {
		t.Errorf("want 2 red self-IPs (one per TMM), got %d", n)
	}
	if n := strings.Count(body, greenSubnet+"."); n != 2 {
		t.Errorf("want 2 green self-IPs (one per TMM), got %d", n)
	}
}

func TestRenderRoutingTemplateReadsVlans(t *testing.T) {
	body := renderRoutingTemplate()

	// The controller renders with plain text/template and no Sprig, so any
	// helper (list, dict, default, …) fails the whole render with
	// `function "x" not defined` and leaves TMM unconfigured.
	if !strings.Contains(body, "range $name, $v := .vlans") {
		t.Error("template does not range over .vlans")
	}
	for _, sprig := range []string{"list ", "dict ", "default ", "toYaml", "hasKey"} {
		if strings.Contains(body, "{{- "+sprig) || strings.Contains(body, "("+sprig) {
			t.Errorf("template uses Sprig helper %q — the renderer has no Sprig", sprig)
		}
	}
	for _, f := range []string{"selfIPv4", "selfIPv6", "prefixLenV4", "prefixLenV6", "tag", "vrf"} {
		if !strings.Contains(body, "$v."+f) {
			t.Errorf("template does not render .%s — the assertion for it can never fail", f)
		}
	}
	// The context dump must survive Sprintf as a live template action.
	if !strings.Contains(body, `{{ printf "%v" . }}`) {
		t.Error("context dump directive was mangled by Sprintf escaping")
	}
}

func TestRenderGlobalRoutingConfig(t *testing.T) {
	body := renderGlobalRoutingConfig()
	// spec.bgp is rejected outright without routerIdSource (or
	// routerIdSubnet+routerIdPrefix).
	if !strings.Contains(body, `routerIdSource: "%POD_IP%"`) {
		t.Error("GlobalRoutingConfig omits routerIdSource — the API server rejects the CR")
	}
	if !strings.Contains(body, "name: "+tmplName) {
		t.Error("GlobalRoutingConfig does not bind the RoutingTemplate")
	}
}

func TestRenderVrfs(t *testing.T) {
	body := renderVrfs()
	// kind is Vrf, not VRF — `kind: VRF` fails with "no matches for kind".
	if strings.Contains(body, "kind: VRF") {
		t.Error("kind must be Vrf; VRF does not resolve")
	}
	if n := strings.Count(body, "kind: Vrf"); n != 2 {
		t.Errorf("want both tenant Vrfs, got %d", n)
	}
	if !strings.Contains(body, "name: "+vrfRed) || !strings.Contains(body, "name: "+vrfGreen) {
		t.Error("missing a tenant Vrf")
	}
}

// The next-hop probe is the difference between "the template rendered the
// right string" and "that address is reachable on that tenant's VLAN".
func TestProbeSetupScript(t *testing.T) {
	sc := probeSetupScript()
	for _, want := range []string{
		"ip link add " + probeVeth + " type veth peer name " + probeBrPeer,
		"master " + deploy.DAGBridge,             // the bridge TMM's 1.1 sits on
		"type vlan id 2001", "type vlan id 2002", // one subinterface per tenant tag
		redSubnet + ".254/24", greenSubnet + ".254/24",
	} {
		if !strings.Contains(sc, want) {
			t.Errorf("probe setup missing %q:\n%s", want, sc)
		}
	}
	// Re-runnable: a leftover veth from a previous run must be cleared, or the
	// second run fails with "File exists" and every probe assertion fails.
	if !strings.Contains(sc, "ip link del "+probeVeth) {
		t.Error("probe setup does not delete a leftover veth first")
	}
	if !strings.Contains(probeTeardownScript(), "ip link del "+probeVeth) {
		t.Error("teardown does not remove the veth — it lives in the node netns and would linger")
	}
	// IFNAMSIZ is 15: the VLAN subinterface name is probeVeth + "." + tag, and
	// the kernel rejects the whole setup with "not a valid ifname" if it is
	// longer. probeBrPeer must fit too.
	for _, n := range []string{
		fmt.Sprintf("%s.%d", probeVeth, tagRed),
		fmt.Sprintf("%s.%d", probeVeth, tagGreen),
		probeBrPeer,
	} {
		if len(n) > 15 {
			t.Errorf("interface name %q is %d chars, kernel limit is 15", n, len(n))
		}
	}
}

// arping exits non-zero when nothing answers, which is the EXPECTED result for
// the isolation probe — so the reply count is the signal, not the exit status.
func TestArpReplied(t *testing.T) {
	replied := `ARPING 198.51.100.11 from 198.51.100.254 scn-vlprobe.2001
Unicast reply from 198.51.100.11 [02:00:00:00:00:01]  0.750ms
Sent 3 probes (1 broadcast(s))
Received 1 response(s)
3 packets transmitted, 1 packets received,  66% unanswered`
	if !arpReplied(replied) {
		t.Error("a reply was not detected")
	}

	silent := `ARPING 198.51.101.11 from 198.51.100.254 scn-vlprobe.2001
Sent 3 probes (1 broadcast(s))
Received 0 response(s)
3 packets transmitted, 0 packets received, 100% unanswered`
	if arpReplied(silent) {
		t.Error("silence was reported as a reply — tenant isolation would pass spuriously")
	}
	if arpReplied("arping: command not found") {
		t.Error("a missing tool was reported as a reply")
	}
}
