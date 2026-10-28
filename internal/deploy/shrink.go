package deploy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mwiget/ocibnkctl/internal/version"
	"github.com/mwiget/ocibnkctl/kit"
)

// ShrinkPolicyName is the Kyverno ClusterPolicy name the shrink step
// installs. Exported so callers can report / delete it.
const ShrinkPolicyName = "f5-bnk-shrink-requests"

// Default per-container request ceilings applied by the shrink policy.
// Requests are scheduling reservations, not usage caps (limits are left
// untouched), so values far below real BNK usage are safe on a single-host
// demo cluster — they just stop the chart from reserving ~28Gi / ~17 cores
// it never uses.
const (
	DefaultShrinkCPURequest    = "25m"
	DefaultShrinkMemoryRequest = "128Mi"
)

// shrinkRequestsJSONPath reads the per-container request ceilings back out of
// an applied shrink policy (the first container patch of its only rule).
const shrinkRequestsJSONPath = `{.spec.rules[?(@.name=="shrink-f5-requests")].mutate.foreach[0].patchStrategicMerge.spec.containers[0].resources.requests.cpu} ` +
	`{.spec.rules[?(@.name=="shrink-f5-requests")].mutate.foreach[0].patchStrategicMerge.spec.containers[0].resources.requests.memory}`

// ActiveShrinkRequests returns the CPU and memory request ceilings of the
// shrink policy applied to the cluster; ok is false when none is (Kyverno
// absent, policy not applied). Components installed after `deploy shrink` —
// Multus, which e2e only installs in deploy cne — use it to pick up the cap
// the shrink step could not apply to them yet.
func ActiveShrinkRequests(ctx context.Context, r *Runner) (cpu, memory string, ok bool) {
	raw, err := r.KubectlCapture(ctx, "get", "clusterpolicy", ShrinkPolicyName,
		"-o", "jsonpath="+shrinkRequestsJSONPath)
	if err != nil {
		return "", "", false
	}
	return parseShrinkRequests(raw)
}

func parseShrinkRequests(raw string) (cpu, memory string, ok bool) {
	f := strings.Fields(raw)
	if len(f) != 2 {
		return "", "", false
	}
	return f[0], f[1], true
}

// ShrinkInputs are substituted into the embedded Kyverno policy template.
type ShrinkInputs struct {
	SharedComponentNamespace string // f5-cne-core
	OperatorNamespace        string // f5-operators (FLO)
	CPURequest               string // per-container CPU request ceiling
	MemoryRequest            string // per-container memory request ceiling
}

// RenderShrinkPolicy renders the f5-bnk-shrink-requests ClusterPolicy.
// Zero-value fields fall back to the canonical namespaces and the default
// request ceilings.
func RenderShrinkPolicy(in ShrinkInputs) (string, error) {
	if in.SharedComponentNamespace == "" {
		in.SharedComponentNamespace = SharedComponentNamespace
	}
	if in.OperatorNamespace == "" {
		in.OperatorNamespace = "f5-operators"
	}
	if in.CPURequest == "" {
		in.CPURequest = DefaultShrinkCPURequest
	}
	if in.MemoryRequest == "" {
		in.MemoryRequest = DefaultShrinkMemoryRequest
	}
	return kit.Render("platform/shrink/kyverno-shrink-requests.yaml", map[string]string{
		"SHARED_NAMESPACE":   in.SharedComponentNamespace,
		"OPERATOR_NAMESPACE": in.OperatorNamespace,
		"CPU_REQUEST":        in.CPURequest,
		"MEMORY_REQUEST":     in.MemoryRequest,
	})
}

// KyvernoHelmArgs trims the Kyverno chart to the admission controller.
var KyvernoHelmArgs = []string{
	"--set", "backgroundController.enabled=false",
	"--set", "cleanupController.enabled=false",
	"--set", "reportsController.enabled=false",
	"--set", "admissionController.replicas=1",
}

// InstallKyverno installs (or upgrades) Kyverno trimmed to just the
// admission controller — mutate-on-admission needs nothing else, and
// disabling the background / reports / cleanup controllers keeps Kyverno's
// own footprint to a single small pod. Idempotent via `helm upgrade
// --install`.
func InstallKyverno(ctx context.Context, r *Runner) error {
	if err := r.HelmUpgrade(ctx,
		"kyverno", version.KyvernoChart, version.KyvernoRepo,
		"kyverno", version.KyvernoVersion, "",
		KyvernoHelmArgs...,
	); err != nil {
		return err
	}
	// HelmUpgrade no longer passes --wait (it wedges on docker-proxy k3s); gate
	// Kyverno's admission controller readiness with kubectl wait instead, so
	// mutate-on-admission is live before the next phase applies resources.
	if err := r.Wait(ctx, "kyverno", "Available",
		"deployment/kyverno-admission-controller", 5*time.Minute); err != nil {
		fmt.Fprintf(r.Out, "      | warn: kyverno admission controller not Available yet: %v\n", err)
	}
	return nil
}
