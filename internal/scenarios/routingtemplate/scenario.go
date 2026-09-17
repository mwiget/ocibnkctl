// Package routingtemplate implements scenario "routing-template-multitenant" —
// the F5 design "Expose VLAN/Self-IPs in Routing template": a top-level
// `.vlans` variable that makes each TMM's OWN per-VLAN self-IP addressable
// from a RoutingTemplate, keyed by F5SPKVlanSpec.name.
//
//	.vlans.<name>.selfIPv4 / .selfIPv6 / .prefixLenV4 / .prefixLenV6 / .tag / .vrf
//
// The multi-tenant shape is two tagged VLANs on the SAME interface (1.1),
// each bound to its own Vrf — so one template renders per-tenant routing
// config from per-TMM addresses the operator never has to enumerate.
//
// Requires BNK 2.4's LEGACY SPK network model (cneController env
// USE_GATEWAY_SETTINGS=false), which is what bnk.tmm_dataplane_mode=selfip-dag
// renders. In the Infra/GatewaySettings model the F5SPKVlan CRs are accepted
// by the API server and then never reconciled, so `.vlans` is empty — Verify
// says so explicitly rather than reporting a mystery failure.
package routingtemplate

import (
	"fmt"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

const (
	scnName  = "routing-template-multitenant"
	scnTitle = "Expose VLAN/Self-IPs in a RoutingTemplate (.vlans) — two tagged tenants on one interface"

	// Tenants: one Vrf + one tagged F5SPKVlan each, both on interface 1.1.
	vrfRed    = "red"
	vrfGreen  = "green"
	vlanRed   = "tenant-red"
	vlanGreen = "tenant-green"
	tagRed    = 2001
	tagGreen  = 2002
	prefixV4  = 24
	prefixV6  = 112

	// TMM interface the VLANs bind. mapres puts net1 here when
	// TMM_MAPRES_ADDL_VETHS_ON_DP=TRUE and leaves it bare for the VLAN CR.
	vlanInterface = "1.1"

	tmplName = "scn-rt-vlans"
	grcName  = "scn-grc-vlans"

	fileVrfs     = "01-vrfs.yaml"
	fileVlans    = "02-vlans.yaml"
	fileTemplate = "03-routing-template.yaml"
	fileGRC      = "04-global-routing-config.yaml"

	// Marker prefixes in the rendered ZeBOS config. Verify greps these back
	// out of each TMM, which is the only way to observe per-device rendering.
	ctxMarker  = "! OCIBNKCTL-CTX "
	vlanMarker = "! OCIBNKCTL-VLAN "
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string  { return scnName }
func (s *scenario) Title() string { return scnTitle }

// Amber, not green: every assertion here passes, but only on a cluster in
// BNK 2.4's legacy SPK network model (bnk.tmm_dataplane_mode: selfip-dag →
// USE_GATEWAY_SETTINGS=false). `scenario run --all` runs the green set against
// the default anycast-bgp shape, where the F5SPKVlan this needs is never
// reconciled — so amber keeps it out of --all while leaving it runnable by
// name on the right cluster.
func (s *scenario) Rating() scenarios.Rating { return scenarios.Amber }
func (s *scenario) Dependencies() []string   { return nil }

func (s *scenario) Description() string {
	return strings.TrimSpace(`
Exercises the F5 design "Expose VLAN/Self-IPs in Routing template": VLAN
self-IPs are allocated per TMM by the controller, and a new top-level
.vlans template variable makes each TMM's own self-IP addressable from a
RoutingTemplate — following the existing .global.tmmAllocatedIPs pattern.

Multi-tenant shape: two tagged VLANs on the SAME TMM interface (1.1),
tenant-red (tag 2001, vrf red) and tenant-green (tag 2002, vrf green),
each with one self-IP per TMM. One RoutingTemplate then renders both
tenants' routing config from .vlans, and the controller renders it once
per TMM device with that device's own addresses.

Asserts: both Vrf CRs and both F5SPKVlans reach Programmed; TMM binds both
VLANs to interface 1.1 with no declTmm error; every TMM's rendered config
carries all six documented .vlans fields for both tenants; each TMM renders
ITS OWN self-IP (element order of selfip_v4s maps to DP-0, DP-1, …); and the
two tenants stay distinct (red never renders green's address or vrf).

REQUIRES the legacy SPK network model — bnk.tmm_dataplane_mode: selfip-dag,
which renders USE_GATEWAY_SETTINGS=false. BNK 2.4 ships two mutually
exclusive network models and only that one reconciles F5SPKVlan; under
Infra/GatewaySettings the CRs are accepted and silently ignored, leaving
.vlans empty. Verify detects that and reports it as the cause rather than
as a failed assertion.

Cleanup deletes the GlobalRoutingConfig, RoutingTemplate, both VLANs and
both Vrfs. Note GlobalRoutingConfig is a CLUSTER SINGLETON ("Only one
GlobalRoutingConfig instance is allowed"), so Apply removes a stale one
left behind by an earlier run.
`)
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	n := ctx.PoC.Cluster.Workers()
	if n < 1 {
		n = 1
	}
	var paths []string
	for _, m := range []struct{ name, body string }{
		{fileVrfs, renderVrfs()},
		{fileVlans, renderVlans(n)},
		{fileTemplate, renderRoutingTemplate()},
		{fileGRC, renderGlobalRoutingConfig()},
	} {
		p, err := scenarios.WriteManifest(ctx.PoCDir, scnName, m.name, m.body)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// Each tenant gets its OWN subnet, not two ranges inside one /24. That is what
// makes the next-hop probe unambiguous: the peer holds one address per tenant
// VLAN, so a reply can only have come from the VLAN it was sent on.
//
// Element order in selfip_v4s is the contract: index i binds to device DP-i.
const (
	redSubnet   = "198.51.100"
	greenSubnet = "198.51.101"
	// The probe peer's address on each tenant VLAN, created in the node netns.
	probeHost  = 254
	selfIPBase = 11
)

func redV4(i int) string   { return fmt.Sprintf("%s.%d", redSubnet, selfIPBase+i) }
func greenV4(i int) string { return fmt.Sprintf("%s.%d", greenSubnet, selfIPBase+i) }
func redV6(i int) string   { return fmt.Sprintf("2001:db8:1::%d", selfIPBase+i) }
func greenV6(i int) string { return fmt.Sprintf("2001:db8:2::%d", selfIPBase+i) }

// renderVrfs declares the two tenants. The Vrf CR is the legacy-model
// counterpart of Infra.spec.vrfs[]; an F5SPKVlan referencing an unknown vrf
// is rejected, so these go first.
func renderVrfs() string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1
kind: Vrf
metadata:
  name: %s
  namespace: default
spec:
  strictIsolation: true
---
apiVersion: k8s.f5net.com/v1
kind: Vrf
metadata:
  name: %s
  namespace: default
spec:
  strictIsolation: true
`, vrfRed, vrfGreen)
}

// renderVlans emits one tagged VLAN per tenant, both on interface 1.1, each
// with one self-IP per TMM. selfip_v6s is populated so all six .vlans fields
// carry a real value (the CRD's IPv6 pattern rejects non-hex labels, so the
// addresses are plain 2001:db8:1::N).
func renderVlans(n int) string {
	var b strings.Builder
	for _, t := range []struct {
		name   string
		tag    int
		vrf    string
		v4, v6 func(int) string
	}{
		{vlanRed, tagRed, vrfRed, redV4, redV6},
		{vlanGreen, tagGreen, vrfGreen, greenV4, greenV6},
	} {
		if b.Len() > 0 {
			b.WriteString("---\n")
		}
		fmt.Fprintf(&b, `apiVersion: k8s.f5net.com/v1
kind: F5SPKVlan
metadata:
  name: %s
  namespace: default
spec:
  name: %s
  interfaces: ["%s"]
  tag: %d
  vrf: %s
  mtu: 1500
  prefixlen_v4: %d
  prefixlen_v6: %d
  selfip_v4s:
`, t.name, t.name, vlanInterface, t.tag, t.vrf, prefixV4, prefixV6)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "    - %q\n", t.v4(i))
		}
		b.WriteString("  selfip_v6s:\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "    - %q\n", t.v6(i))
		}
	}
	return b.String()
}

// renderRoutingTemplate is the scenario's actual subject: a template that
// reads .vlans. Every VLAN's six fields go onto one marker line so Verify can
// parse them back per TMM, and the whole context is dumped on another so a
// failure shows exactly which keys the build offers.
//
// It ranges over .vlans rather than indexing by name: the controller renders
// with plain text/template and NO Sprig, so `list` (and the rest of the Sprig
// helpers) are undefined — using one fails the whole render with
// `function "list" not defined` and leaves TMM unconfigured. Ranging also
// means a build without the feature simply emits no marker lines, which is
// exactly the "absent" signal Verify reports.
func renderRoutingTemplate() string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1alpha1
kind: RoutingTemplate
metadata:
  name: %[1]s
  namespace: default
spec:
  body: |
    route-map RMALL permit 10
    !
    %[2]s{{ printf "%%v" . }}
    {{- range $name, $v := .vlans }}
    %[3]s{{ $name }} selfIPv4={{ $v.selfIPv4 }} selfIPv6={{ $v.selfIPv6 }} prefixLenV4={{ $v.prefixLenV4 }} prefixLenV6={{ $v.prefixLenV6 }} tag={{ $v.tag }} vrf={{ $v.vrf }}
    {{- end }}
    !
    router bgp {{ .global.spec.bgp.startASN }}
      bgp router-id {{ .global.spec.bgp.routerIdSource }}
    {{- range $name, $v := .vlans }}
    {{- if $v.vrf }}
      ! tenant {{ $v.vrf }} sources from its own per-TMM self-IP
      neighbor {{ $v.selfIPv4 }} description tenant-{{ $v.vrf }}-selfip
    {{- end }}
    {{- end }}
    exit
    !
`, tmplName, ctxMarker, vlanMarker)
}

// renderGlobalRoutingConfig binds the template. spec.bgp requires
// routerIdSource (or routerIdSubnet+routerIdPrefix); %POD_IP% arrives in the
// template context already expanded to the rendering TMM's pod IP.
func renderGlobalRoutingConfig() string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1alpha1
kind: GlobalRoutingConfig
metadata:
  name: %s
  namespace: default
spec:
  templateRef:
    name: %s
  bgp:
    startASN: 65000
    routerIdSource: "%%POD_IP%%"
`, grcName, tmplName)
}

func (s *scenario) Apply(ctx *scenarios.Context) error {
	r := ctx.Runner

	if !ctx.PoC.BNK.IsSelfIPDAG() {
		return fmt.Errorf("this scenario needs BNK 2.4's legacy SPK network model "+
			"(USE_GATEWAY_SETTINGS=false), which only bnk.tmm_dataplane_mode=%s renders; "+
			"this PoC is %q, where F5SPKVlan CRs are accepted and never reconciled",
			poc.DataplaneSelfIPDAG, ctx.PoC.BNK.DataplaneMode())
	}

	// GlobalRoutingConfig is a cluster singleton — a stale one from an earlier
	// run makes every apply here fail admission. Both it and RoutingTemplate
	// are immutable, so the only way to re-run is delete + recreate.
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "globalroutingconfig", grcName, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingtemplate", tmplName, "--ignore-not-found")

	for _, f := range []string{fileVrfs, fileVlans, fileTemplate, fileGRC} {
		body, err := scenarios.ReadManifest(ctx.PoCDir, scnName, f)
		if err != nil {
			return err
		}
		if err := r.Apply(ctx.Ctx, body); err != nil {
			return fmt.Errorf("apply %s: %w", f, err)
		}
		// VLANs must be programmed before the template renders, or .vlans is
		// populated for only some tenants.
		if f == fileVlans {
			waitProgrammed(ctx, "f5-spk-vlans", []string{vlanRed, vlanGreen}, 2*time.Minute)
		}
	}
	return nil
}

// waitProgrammed polls the named CRs until each reports Programmed=True.
func waitProgrammed(ctx *scenarios.Context, kind string, names []string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		all := true
		for _, n := range names {
			if programmedStatus(ctx, kind, n) != "True" {
				all = false
				break
			}
		}
		if all || time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func programmedStatus(ctx *scenarios.Context, kind, name string) string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", kind, name,
		"-o", `jsonpath={.status.conditions[?(@.type=="Programmed")].status}`)
	return strings.TrimSpace(out)
}

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	res := scenarios.Result{}

	// --- tenants + VLANs reconciled ----------------------------------------
	for _, v := range []string{vrfRed, vrfGreen} {
		got := programmedStatus(ctx, "vrf", v)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("Vrf %q Programmed=True", v),
			OK:          got == "True",
			Got:         got,
		})
	}
	for _, v := range []string{vlanRed, vlanGreen} {
		got := programmedStatus(ctx, "f5-spk-vlans", v)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("F5SPKVlan %q Programmed=True (config sent to every TMM)", v),
			OK:          got == "True",
			Got:         got,
		})
	}

	pods, err := tmmPods(ctx)
	if err != nil || len(pods) == 0 {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "list Running f5-tmm pods", OK: false, Got: errString(err),
		})
		return finalize(res)
	}

	// --- TMM actually bound them -------------------------------------------
	for i, p := range pods {
		vm, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "exec", p,
			"-c", "debug", "--", "tmctl", "-d", "blade", "vlan_member_stat")
		boundRed := strings.Contains(vm, vlanRed) && strings.Contains(vm, vlanInterface)
		boundGreen := strings.Contains(vm, vlanGreen)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d bound both VLANs to interface %s", i+1, len(pods), vlanInterface),
			OK:          boundRed && boundGreen,
			Got:         oneLine(vm, 200),
		})
	}

	// --- the feature: .vlans in the rendered template -----------------------
	renders := make(map[string]string, len(pods))
	for _, p := range pods {
		renders[p] = pollRender(ctx, p, 2*time.Minute)
	}

	dump := firstLineWith(renders, ctxMarker)
	keys := contextKeys(dump)
	hasVlans := hasKey(keys, "vlans")
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "RoutingTemplate context exposes .vlans (the feature under test)",
		OK:          hasVlans,
		Got:         fmt.Sprintf("context keys = %v", keys),
	})
	if !hasVlans {
		res.Details = "`.vlans` is empty: BNK 2.4 only reconciles F5SPKVlan in the legacy SPK " +
			"network model. Check the CNE controller's USE_GATEWAY_SETTINGS is false " +
			"(bnk.tmm_dataplane_mode: selfip-dag)."
		return finalize(res)
	}

	// Per-TMM: each device must render ITS OWN self-IP, and the two tenants
	// must stay distinct. selfip_v4s[i] binds DP-i, so pod i expects index i.
	for i, p := range pods {
		fields := parseVlanMarkers(renders[p])
		for _, t := range []struct {
			name, vrf string
			tag       int
			wantV4    string
			wantV6    string
			otherIPv4 string
		}{
			{vlanRed, vrfRed, tagRed, redV4(i), redV6(i), greenV4(i)},
			{vlanGreen, vrfGreen, tagGreen, greenV4(i), greenV6(i), redV4(i)},
		} {
			f := fields[t.name]
			okAll := f["selfIPv4"] == t.wantV4 &&
				f["selfIPv6"] == t.wantV6 &&
				f["prefixLenV4"] == fmt.Sprint(prefixV4) &&
				f["prefixLenV6"] == fmt.Sprint(prefixV6) &&
				f["tag"] == fmt.Sprint(t.tag) &&
				f["vrf"] == t.vrf
			res.Assertions = append(res.Assertions, scenarios.Assertion{
				Description: fmt.Sprintf("TMM %d/%d renders all six .vlans fields for %q (own self-IP %s)",
					i+1, len(pods), t.name, t.wantV4),
				OK:  okAll,
				Got: fmt.Sprintf("%v", f),
			})
			// Tenant isolation: this tenant's line must not carry the other's
			// address — a single shared map would show up here.
			res.Assertions = append(res.Assertions, scenarios.Assertion{
				Description: fmt.Sprintf("tenant %q render excludes %q's self-IP (%s)", t.name, otherOf(t.name), t.otherIPv4),
				OK:          f["selfIPv4"] != t.otherIPv4 && f["selfIPv4"] != "",
				Got:         f["selfIPv4"],
			})
		}
	}

	// --- per-tenant next hop, on the wire -----------------------------------
	// Everything above is control plane. This puts a peer on each tenant VLAN
	// and checks the self-IP actually answers there — and, just as important,
	// that the other tenant's address does NOT answer on the wrong VLAN.
	nodes, err := tmmNodes(ctx)
	if err != nil {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "map TMM pods to their nodes for the next-hop probe",
			OK:          false, Got: errString(err),
		})
		return finalize(res)
	}
	for i, p := range pods {
		node := nodes[p]
		if node == "" {
			res.Assertions = append(res.Assertions, scenarios.Assertion{
				Description: fmt.Sprintf("TMM %d/%d: resolve node for the next-hop probe", i+1, len(pods)),
				OK:          false, Got: "no nodeName",
			})
			continue
		}
		setup, err := scenarios.NodeNetnsRun(ctx, node, "sh", "-c", probeSetupScript())
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d: tenant VLAN peer built on %s (%s.%d + %s.%d on %s)",
				i+1, len(pods), node, probeVeth, tagRed, probeVeth, tagGreen, deploy.DAGBridge),
			OK:  err == nil && strings.Contains(setup, redSubnet),
			Got: oneLine(setup, 200),
		})
		if err != nil {
			continue
		}

		for _, t := range []struct {
			tenant string
			tag    int
			target string
		}{
			{vlanRed, tagRed, redV4(i)},
			{vlanGreen, tagGreen, greenV4(i)},
		} {
			ok, out := arpProbe(ctx, node, t.tag, t.target, true)
			res.Assertions = append(res.Assertions, scenarios.Assertion{
				Description: fmt.Sprintf("TMM %d/%d: %s self-IP %s answers ARP on VLAN %d (it is a real next hop)",
					i+1, len(pods), t.tenant, t.target, t.tag),
				OK:  ok,
				Got: out,
			})
		}

		// Isolation, the other half of "per tenant": green's self-IP must be
		// unreachable from red's VLAN. A shared L2 or a collapsed route domain
		// would answer here.
		ok, out := arpProbe(ctx, node, tagRed, greenV4(i), false)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d: %s self-IP %s is SILENT on %s's VLAN %d (tenant isolation)",
				i+1, len(pods), vlanGreen, greenV4(i), vlanRed, tagRed),
			OK:  ok,
			Got: out,
		})
	}

	return finalize(res)
}

func otherOf(name string) string {
	if name == vlanRed {
		return vlanGreen
	}
	return vlanRed
}

// parseVlanMarkers turns the rendered marker lines
//
//	! OCIBNKCTL-VLAN tenant-red selfIPv4=... selfIPv6=... tag=... vrf=...
//
// into map[vlan]map[field]value. Values may be empty (a build without the
// feature renders "<no value>"), which the callers treat as a failure.
func parseVlanMarkers(body string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		i := strings.Index(line, vlanMarker)
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+len(vlanMarker):])
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		kv := map[string]string{}
		for _, f := range fields[1:] {
			if k, v, ok := strings.Cut(f, "="); ok {
				kv[k] = v
			}
		}
		out[name] = kv
	}
	return out
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	r := ctx.Runner
	// GRC first — it holds the template through a finalizer.
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "globalroutingconfig", grcName, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingtemplate", tmplName, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "f5-spk-vlans", vlanRed, vlanGreen, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "vrf", vrfRed, vrfGreen, "--ignore-not-found")

	// The probe veth lives in the NODE's netns, not in a container that went
	// away with the scenario — it has to be removed explicitly or it lingers
	// on the bridge across runs.
	if nodes, err := tmmNodes(ctx); err == nil {
		for _, node := range nodes {
			_, _ = scenarios.NodeNetnsRun(ctx, node, "sh", "-c", probeTeardownScript())
		}
	}
	return nil
}

// --- per-tenant next-hop probe ---------------------------------------------
//
// Rendering the right self-IP into a template proves the control plane. It
// does NOT prove anything reaches that address. These helpers put a peer on
// the tenant VLANs and ARP for each self-IP, which is the minimum honest
// evidence that a tenant self-IP is a real next hop.
//
// ARP rather than ICMP on purpose: an F5 self-IP answers ARP unconditionally
// because it must, in order to receive any traffic at all, whereas ICMP is
// subject to the VLAN's allowed_services lockdown (empty by default here).
// A ping-based probe would fail on a perfectly working self-IP.
//
// The peer is a veth into br-f5-tmm-dag — the per-node bridge TMM's net1
// (interface 1.1) sits on — carrying one 802.1Q subinterface per tenant tag.
// Linux bridges do not filter VLANs by default, so tagged frames reach TMM's
// 1.1 and are matched by the F5SPKVlan with that tag.
const (
	// Kernel ifnames cap at 15 chars (IFNAMSIZ), and the VLAN subinterfaces
	// append ".<tag>" — so the base name has to leave room for 5 more.
	probeVeth   = "scn-vlp"
	probeBrPeer = "scn-vlp-br"
)

// probeSetupScript builds the peer inside a node's netns. Idempotent: it
// deletes a leftover veth first, and deleting one end removes both.
func probeSetupScript() string {
	return fmt.Sprintf(`set -e
ip link del %[1]s 2>/dev/null || true
ip link add %[1]s type veth peer name %[2]s
ip link set %[2]s master %[3]s up
ip link set %[1]s up
ip link add link %[1]s name %[1]s.%[4]d type vlan id %[4]d
ip link add link %[1]s name %[1]s.%[5]d type vlan id %[5]d
ip addr add %[6]s.%[8]d/%[9]d dev %[1]s.%[4]d
ip addr add %[7]s.%[8]d/%[9]d dev %[1]s.%[5]d
ip link set %[1]s.%[4]d up
ip link set %[1]s.%[5]d up
ip -br addr show | grep %[1]s`,
		probeVeth, probeBrPeer, deploy.DAGBridge,
		tagRed, tagGreen, redSubnet, greenSubnet, probeHost, prefixV4)
}

func probeTeardownScript() string {
	return fmt.Sprintf("ip link del %s 2>/dev/null || true; echo removed", probeVeth)
}

// arpProbe sends ARP requests for target out of the tenant VLAN carrying tag,
// and reports whether the address answered.
func arpProbe(ctx *scenarios.Context, node string, tag int, target string, replies bool) (bool, string) {
	// -c 3 -w 5: three requests, give up after five seconds. arping exits
	// non-zero when nothing answers, which is the expected outcome for the
	// isolation probe, so the exit status is not the signal — the reply count
	// in the output is.
	out, _ := scenarios.NodeNetnsRun(ctx, node, "sh", "-c",
		fmt.Sprintf("arping -I %s.%d -c 3 -w 5 %s 2>&1 || true", probeVeth, tag, target))
	got := arpReplied(out)
	return got == replies, oneLine(out, 180)
}

// arpReplied reports whether an arping run saw at least one reply. Its summary
// line is "N packets transmitted, M packets received"; anything with M > 0, or
// a "Unicast reply from" line, counts.
func arpReplied(out string) bool {
	if strings.Contains(out, "Unicast reply from") {
		return true
	}
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "packets received")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[:i])
		if len(fields) == 0 {
			continue
		}
		if n := fields[len(fields)-1]; n != "0" {
			return true
		}
	}
	return false
}

// tmmNodes maps each Running TMM pod to the node it runs on. The tenant
// bridge is per node, so the probe has to run on the node hosting that TMM.
func tmmNodes(ctx *scenarios.Context) (map[string]string, error) {
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-tmm", "--field-selector=status.phase=Running",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{" "}{.spec.nodeName}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			m[f[0]] = f[1]
		}
	}
	return m, nil
}

// --- helpers ---------------------------------------------------------------

func tmmPods(ctx *scenarios.Context) ([]string, error) {
	out, err := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-tmm", "--field-selector=status.phase=Running",
		"--sort-by=.metadata.creationTimestamp",
		"-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// pollRender reads the rendered GlobalRoutingConfig config back out of one
// TMM's routing container — the only place per-device rendering is observable.
func pollRender(ctx *scenarios.Context, pod string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "exec", pod,
			"-c", "f5-tmm-routing", "--", "sh", "-c",
			"cat /tmm-routing-config/conf-create/default_"+grcName+"_* 2>/dev/null")
		if strings.Contains(out, ctxMarker) {
			return out
		}
		if time.Now().After(deadline) {
			return out
		}
		select {
		case <-ctx.Ctx.Done():
			return out
		case <-time.After(5 * time.Second):
		}
	}
}

// contextKeys extracts the top-level keys of the `{{ printf "%v" . }}` dump,
// i.e. the outermost level of the map[k:v …] Go renders.
func contextKeys(dump string) []string {
	i := strings.Index(dump, "map[")
	if i < 0 {
		return nil
	}
	var keys []string
	depth := 0
	var key strings.Builder
	for _, r := range dump[i+len("map["):] {
		switch {
		case r == '[':
			depth++
		case r == ']':
			if depth == 0 {
				return keys // closing bracket of the outermost map
			}
			depth--
		case depth > 0:
			// inside a nested value — not a top-level key
		case r == ':':
			// Keys are flushed only at a depth-0 colon; flushing at the
			// closing bracket would emit whatever trailed the last value.
			if k := strings.TrimSpace(key.String()); k != "" {
				keys = append(keys, k)
			}
			key.Reset()
		case r == ' ':
			key.Reset()
		default:
			key.WriteRune(r)
		}
	}
	return keys
}

func hasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func firstLineWith(renders map[string]string, marker string) string {
	for _, body := range renders {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, marker) {
				return line
			}
		}
	}
	return ""
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = fmt.Sprintf("two tenants (%s/%s) on interface %s; .vlans renders all six fields per TMM with each device's own self-IP",
			vrfRed, vrfGreen, vlanInterface)
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
