// Package ficdynamicip implements scenario "fic-dynamic-ip" — F5 BNK
// use-case "Dynamic IP address allocation" (FIC for Gateway API).
//
// Same plumbing as http-routing-e2e except the Gateway omits
// spec.addresses entirely and binds, via spec.infrastructure.parametersRef,
// to a GatewaySettings CR (BNK 2.4's replacement for the F5BnkGateway
// pool). The GatewaySettings references the platform Infra CR's IPAM pool
// (default/infra, bnk-dynamic-vips = 203.0.113.110-119, applied by
// `ocibnkctl deploy cne`), and the CNE controller allocates the listener
// address from it. The scenario asserts (a) GatewaySettings resolved,
// (b) gateway.status.addresses got an IP from that pool and the Gateway is
// Programmed, (c) FRR's BGP table learns that /32, (d) 5/5 end-to-end
// curls to the dynamically-allocated address succeed.
//
// Reference: the 2.3 use-case page (…/use-cases/bnk-ficforgatewayapi.html)
// is gone from the 2.4 docs; the 2.4 sources are the GatewaySettings and
// Infra CRD references plus "Configure tenant traffic settings with
// GatewaySettings" under how-tos.
package ficdynamicip

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

//go:embed manifests/*.yaml
var manifestFS embed.FS

const (
	scnName  = "fic-dynamic-ip"
	scnTitle = "Dynamic IP address allocation (FIC for Gateway API) — address-less Gateway + GatewaySettings/Infra IPAM"
)

func init() { scenarios.Register(&scenario{}) }

type scenario struct{}

func (s *scenario) Name() string             { return scnName }
func (s *scenario) Title() string            { return scnTitle }
func (s *scenario) Rating() scenarios.Rating { return scenarios.Green }
func (s *scenario) Dependencies() []string   { return []string{"bgp-peer-frr"} }
func (s *scenario) Description() string {
	return strings.TrimSpace(`
Demonstrates dynamic IP allocation for a Gateway API Gateway.

The Gateway is applied WITHOUT spec.addresses. A GatewaySettings CR in the
same namespace references the platform IPAM pool (Infra default/infra,
ipams bnk-dynamic-vips = 203.0.113.110-119 — applied by ocibnkctl deploy cne
together with USE_GATEWAY_SETTINGS=true on the CNE controller) and the
Gateway binds to it through spec.infrastructure.parametersRef
(group gateway.k8s.f5.com, kind GatewaySettings).

Status on BNK 2.4.0 in ocibnkctl's demo-TMM shape (🟢): the controller
allocates an address from the pool, populates gateway.status.addresses,
programs the listener, OcNOS advertises the /32 to the external FRR and
end-to-end curls succeed. On 2.3.x this was amber — the F5BnkGateway pool
was never bridged into IPAM/IPAMRange CRs in the demo deployment, so the
Gateway stalled at AddressNotAssigned.

Asserts: GatewaySettings Accepted+ResolvedRefs=True; Gateway
Programmed=True with status.addresses inside the pool; HTTPRoute
Accepted=True; FRR learned the allocated /32; 5/5 curls via the external
FRR reach nginx through the allocated VIP.

Cleanup deletes the scn-fic-dyn namespace (the platform Infra CR stays).
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
		base := p[len("manifests/"):]
		out, e := scenarios.WriteManifest(ctx.PoCDir, scnName, base, string(body))
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
		"02-gatewaysettings.yaml",
		"03-backend.yaml",
		"04-gateway.yaml",
		"05-httproute.yaml",
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

	// GatewaySettings resolved against the platform Infra pool.
	gws, _ := r.KubectlCapture(ctx.Ctx, "-n", "scn-fic-dyn", "get",
		"gatewaysettings.gateway.k8s.f5.com/ocibnkctl-fic",
		"-o", `jsonpath={.status.conditions[?(@.type=="Accepted")].status}/{.status.conditions[?(@.type=="ResolvedRefs")].status}`)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "GatewaySettings ocibnkctl-fic Accepted=True + ResolvedRefs=True",
		OK:          strings.TrimSpace(gws) == "True/True",
		Got:         strings.TrimSpace(gws),
	})

	{
		err := r.Wait(ctx.Ctx, "scn-fic-dyn", "Available",
			"deployment/nginx", 3*time.Minute)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "nginx Deployment Available",
			OK:          err == nil, Got: errString(err),
		})
	}

	// Gateway Programmed with an address allocated from the pool.
	{
		err := r.Wait(ctx.Ctx, "scn-fic-dyn", "Programmed",
			"gateway/scn-fic-gateway", 3*time.Minute)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "Gateway Programmed=True (address allocated by the controller)",
			OK:          err == nil, Got: errString(err),
		})
	}
	vip, _ := r.KubectlCapture(ctx.Ctx, "-n", "scn-fic-dyn", "get",
		"gateway/scn-fic-gateway",
		"-o", `jsonpath={.status.addresses[0].value}`)
	vip = strings.TrimSpace(vip)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("gateway.status.addresses populated from the %s pool (%s-%s)",
			deploy.DynamicVIPPool, deploy.DynamicVIPRangeStart, deploy.DynamicVIPRangeEnd),
		OK:  inPool(vip, deploy.DynamicVIPRangeStart, deploy.DynamicVIPRangeEnd),
		Got: vip,
	})

	rstate, _ := r.KubectlCapture(ctx.Ctx, "-n", "scn-fic-dyn", "get",
		"httproute/scn-fic-route",
		"-o", `jsonpath={.status.parents[0].conditions[?(@.type=="Accepted")].status}`)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "HTTPRoute Accepted=True",
		OK:          strings.TrimSpace(rstate) == "True",
		Got:         strings.TrimSpace(rstate),
	})

	if vip == "" {
		return finalize(res)
	}

	// The external FRR must learn the allocated /32 over BGP from a TMM.
	deadline := time.Now().Add(2 * time.Minute)
	var lastTable string
	learned := false
	for time.Now().Before(deadline) {
		lastTable, _ = scenarios.FRRVtysh(ctx, "show bgp ipv4 unicast")
		if strings.Contains(lastTable, vip+"/32") {
			learned = true
			break
		}
		time.Sleep(5 * time.Second)
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "external FRR BGP table has " + vip + "/32 advertised by TMM",
		OK:          learned,
		Got:         oneLine(lastTable, 200),
	})

	// Data plane: curls through the allocated VIP from the FRR netns.
	const curls = 5
	ok := 0
	var last string
	for i := 0; i < curls; i++ {
		body, err := scenarios.FRRNetnsCurl(ctx, "http://"+vip+"/", "-H", "Host: ocibnkctl-fic.local")
		last = oneLine(body, 120)
		if err == nil && strings.Contains(body, "ocibnkctl-scenario-fic-dynamic-ip-OK") {
			ok++
		}
	}
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: fmt.Sprintf("%d/%d curls to the allocated VIP %s reach nginx", curls, curls, vip),
		OK:          ok == curls,
		Got:         fmt.Sprintf("%d/%d — last body: %s", ok, curls, last),
	})

	return finalize(res)
}

// inPool reports whether ip lies within [start, end] (IPv4, inclusive).
func inPool(ip, start, end string) bool {
	a, s, e := net.ParseIP(ip).To4(), net.ParseIP(start).To4(), net.ParseIP(end).To4()
	if a == nil || s == nil || e == nil {
		return false
	}
	return bytes.Compare(a, s) >= 0 && bytes.Compare(a, e) <= 0
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	_ = ctx.Runner.Kubectl(ctx.Ctx, "delete", "namespace", "scn-fic-dyn",
		"--ignore-not-found")
	return nil
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "GatewaySettings resolved; Gateway got a dynamic VIP from the Infra pool, FRR learned it, 5/5 curls succeeded"
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
