package deploy

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/mwiget/ocibnkctl/internal/embedded"
	"github.com/mwiget/ocibnkctl/internal/version"
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
	raw, err := embedded.Templates.ReadFile("templates/kyverno-shrink-requests.yaml.tmpl")
	if err != nil {
		return "", fmt.Errorf("load kyverno-shrink-requests.yaml.tmpl: %w", err)
	}
	tmpl, err := template.New("shrink").Parse(string(raw))
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, in); err != nil {
		return "", err
	}
	return out.String(), nil
}

// InstallKyverno installs (or upgrades) Kyverno trimmed to just the
// admission controller — mutate-on-admission needs nothing else, and
// disabling the background / reports / cleanup controllers keeps Kyverno's
// own footprint to a single small pod. Idempotent via `helm upgrade
// --install`.
func InstallKyverno(ctx context.Context, r *Runner) error {
	return r.HelmUpgrade(ctx,
		"kyverno", version.KyvernoChart, version.KyvernoRepo,
		"kyverno", version.KyvernoVersion, "",
		"--set", "backgroundController.enabled=false",
		"--set", "cleanupController.enabled=false",
		"--set", "reportsController.enabled=false",
		"--set", "admissionController.replicas=1",
	)
}
