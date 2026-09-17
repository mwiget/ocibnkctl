// Package routingconfigtenant implements scenario "routing-config-tenant" —
// per-tenant (VRF-scoped) routing via the namespaced RoutingConfig CR, the
// tenant-side counterpart of the cluster-wide GlobalRoutingConfig.
//
// Where a GlobalRoutingConfig renders one config per TMM device, a
// RoutingConfig renders one per TMM device PER TENANT, into
// /tmm-routing-config/conf-create/tenant/, and its template context gains two
// keys the global one never has: `.tenant` (the RoutingConfig's own spec) and
// a non-zero `.routeDomainID`. `.vlans` is visible in both scopes.
//
// That difference explains the `ip vrf <no value>` in F5's own design doc:
// its example uses `.tenant.spec.vrf` in a GLOBAL template, where `.tenant`
// does not exist.
package routingconfigtenant

import (
	"fmt"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
)

const (
	scnName  = "routing-config-tenant"
	scnTitle = "Per-tenant routing (RoutingConfig) — .tenant + per-tenant route domain in the template context"

	// Reuses the tenant routing-template-multitenant programs, so .vlans is
	// populated when this renders.
	vrfName  = "red"
	tmplName = "scn-rt-tenant"
	rcName   = "scn-rc-tenant"

	fileTemplate = "01-routing-template.yaml"
	fileConfig   = "02-routing-config.yaml"

	ctxMarker = "! OCIBNKCTL-TENANT-CTX "
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
func (s *scenario) Dependencies() []string   { return []string{"routing-template-multitenant"} }

func (s *scenario) Description() string {
	return strings.TrimSpace(`
RoutingConfig is the VRF-scoped half of BNK 2.4 routing: it binds a
RoutingTemplate to one tenant (spec.vrf) and renders per TMM device into
/tmm-routing-config/conf-create/tenant/.

Asserts: the RoutingConfig reaches Programmed=True; a tenant config is
rendered on every TMM; and the tenant template context carries what the
global one does not —

  .tenant.spec.vrf   the tenant's VRF name (red)
  .routeDomainID     that tenant's route domain, non-zero
  .vlans             still populated, so a tenant template can source from
                     its own per-TMM VLAN self-IP

Depends on routing-template-multitenant for the Vrf and the tagged per-tenant
F5SPKVlans, and therefore on the same legacy SPK network model
(USE_GATEWAY_SETTINGS=false, i.e. bnk.tmm_dataplane_mode: selfip-dag).

KNOWN DEFECT, elsewhere: under the OTHER network model
(USE_GATEWAY_SETTINGS=true, VRFs from Infra.spec.vrfs) the same CR segfaults
the controller —

  panic: runtime error: invalid memory address or nil pointer dereference
    internal/k8s.(*LoadBalancerController).RoutingConfigReconciler
        internal/k8s/controller_routingconfig.go:562

and f5-cne-controller crash-loops, taking f5-validation-svc with it so every
F5 CR apply fails admission with 502. This scenario does not reproduce that —
it runs in the legacy model, where the same CR is handled cleanly. See
docs/bnk-2.4.0-doc-errata.md.
`)
}

func (s *scenario) Manifests(ctx *scenarios.Context) ([]string, error) {
	var paths []string
	for _, m := range []struct{ name, body string }{
		{fileTemplate, renderTemplate()},
		{fileConfig, renderRoutingConfig()},
	} {
		p, err := scenarios.WriteManifest(ctx.PoCDir, scnName, m.name, m.body)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// renderTemplate dumps the tenant context and exercises .tenant.spec.vrf the
// way the design doc intends — in a TENANT template, where the key exists.
// Plain text/template, no Sprig: any helper fails the whole render.
func renderTemplate() string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1alpha1
kind: RoutingTemplate
metadata:
  name: %[1]s
  namespace: default
spec:
  body: |
    !
    %[2]s{{ printf "%%v" . }}
    !
    ip vrf {{ .tenant.spec.vrf }}
    exit
    !
`, tmplName, ctxMarker)
}

func renderRoutingConfig() string {
	return fmt.Sprintf(`apiVersion: k8s.f5net.com/v1alpha1
kind: RoutingConfig
metadata:
  name: %s
  namespace: default
spec:
  vrf: %s
  templateRef:
    name: %s
`, rcName, vrfName, tmplName)
}

func (s *scenario) Apply(ctx *scenarios.Context) error {
	r := ctx.Runner

	if !ctx.PoC.BNK.IsSelfIPDAG() {
		return fmt.Errorf("this scenario needs BNK 2.4's legacy SPK network model "+
			"(USE_GATEWAY_SETTINGS=false), which only bnk.tmm_dataplane_mode=%s renders; "+
			"this PoC is %q, where the same CR panics the controller",
			poc.DataplaneSelfIPDAG, ctx.PoC.BNK.DataplaneMode())
	}
	// RoutingTemplate is immutable — delete + recreate is the only re-run path.
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingconfig", rcName, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingtemplate", tmplName, "--ignore-not-found")

	for _, f := range []string{fileTemplate, fileConfig} {
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

func (s *scenario) Verify(ctx *scenarios.Context) scenarios.Result {
	res := scenarios.Result{}

	// The status settles through "CR config deployment is in progress" before
	// it reaches Programmed=True, so this polls rather than reading once.
	got := pollCond(ctx, "routingconfig", rcName, "Programmed", "True", 2*time.Minute)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "RoutingConfig " + rcName + " Programmed=True",
		OK:          got == "True",
		Got:         got,
	})

	// The controller must survive it — this is the CR that panics under the
	// Infra/GatewaySettings model.
	restarts, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-cne-controller",
		"-o", `jsonpath={.items[0].status.containerStatuses[0].restartCount}`)
	phase, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", "pod",
		"-l", "app=f5-cne-controller", "-o", `jsonpath={.items[0].status.phase}`)
	res.Assertions = append(res.Assertions, scenarios.Assertion{
		Description: "f5-cne-controller still Running after the tenant RoutingConfig (no reconciler panic)",
		OK:          strings.TrimSpace(phase) == "Running",
		Got:         fmt.Sprintf("phase=%s restarts=%s", strings.TrimSpace(phase), strings.TrimSpace(restarts)),
	})

	pods, _ := tmmPods(ctx)
	if len(pods) == 0 {
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: "list Running f5-tmm pods", OK: false,
		})
		return finalize(res)
	}

	for i, p := range pods {
		body := pollTenantRender(ctx, p, 2*time.Minute)
		dump := lineWith(body, ctxMarker)

		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d has a tenant render under conf-create/tenant/", i+1, len(pods)),
			OK:          dump != "",
			Got:         oneLine(body, 120),
		})
		if dump == "" {
			continue
		}
		// `.tenant` exists only in tenant scope — this is what the design
		// doc's global-template example was missing.
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d tenant context carries .tenant.spec.vrf=%s", i+1, len(pods), vrfName),
			OK:          strings.Contains(dump, "tenant:map[spec:map[") && strings.Contains(dump, "vrf:"+vrfName),
			Got:         extract(dump, "tenant:map[", 90),
		})
		rd := extract(dump, "routeDomainID:", 24)
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d tenant context has a NON-ZERO routeDomainID (global scope renders 0)", i+1, len(pods)),
			OK:          rd != "" && !strings.HasPrefix(rd, "routeDomainID:0"),
			Got:         rd,
		})
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d .vlans is available in tenant scope too", i+1, len(pods)),
			OK:          strings.Contains(dump, "vlans:map["),
			Got:         extract(dump, "vlans:map[", 110),
		})
		// The rendered ZeBOS line itself, not just the context.
		res.Assertions = append(res.Assertions, scenarios.Assertion{
			Description: fmt.Sprintf("TMM %d/%d rendered `ip vrf %s` (not `<no value>`)", i+1, len(pods), vrfName),
			OK:          strings.Contains(body, "ip vrf "+vrfName),
			Got:         oneLine(lineWith(body, "ip vrf"), 80),
		})
	}
	return finalize(res)
}

func (s *scenario) Cleanup(ctx *scenarios.Context) error {
	r := ctx.Runner
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingconfig", rcName, "--ignore-not-found")
	_ = r.Kubectl(ctx.Ctx, "-n", "default", "delete", "routingtemplate", tmplName, "--ignore-not-found")
	return nil
}

// --- helpers ---------------------------------------------------------------

// pollCond waits for a condition to reach want, returning whatever it last saw.
func pollCond(ctx *scenarios.Context, kind, name, cond, want string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		got := condStatus(ctx, kind, name, cond)
		if got == want || time.Now().After(deadline) {
			return got
		}
		select {
		case <-ctx.Ctx.Done():
			return got
		case <-time.After(5 * time.Second):
		}
	}
}

func condStatus(ctx *scenarios.Context, kind, name, cond string) string {
	out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "get", kind, name,
		"-o", fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, cond))
	return strings.TrimSpace(out)
}

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

// pollTenantRender reads this tenant's rendered config out of one TMM.
func pollTenantRender(ctx *scenarios.Context, pod string, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := ctx.Runner.KubectlCapture(ctx.Ctx, "-n", "default", "exec", pod,
			"-c", "f5-tmm-routing", "--", "sh", "-c",
			"cat /tmm-routing-config/conf-create/tenant/default_"+rcName+"_* 2>/dev/null")
		if strings.Contains(out, ctxMarker) || time.Now().After(deadline) {
			return out
		}
		select {
		case <-ctx.Ctx.Done():
			return out
		case <-time.After(5 * time.Second):
		}
	}
}

func lineWith(body, marker string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, marker) {
			return l
		}
	}
	return ""
}

// extract returns n bytes of s starting at the first occurrence of key.
func extract(s, key string, n int) string {
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	if i+n > len(s) {
		n = len(s) - i
	}
	return s[i : i+n]
}

func finalize(res scenarios.Result) scenarios.Result {
	if res.AllPassed() {
		res.Status = "ok"
		res.Summary = "tenant RoutingConfig programmed; .tenant.spec.vrf, a per-tenant routeDomainID and .vlans all present in tenant scope"
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
