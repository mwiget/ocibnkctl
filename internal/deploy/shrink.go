package deploy

import (
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
