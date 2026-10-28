package deploy

import (
	"context"
	"fmt"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// EnsureGAIECRDs installs the Gateway API Inference Extension CRDs
// (idempotent). BNK 2.4.0's F5 Endpoint Picker consumes InferencePool
// (inference.networking.k8s.io/v1) but ships without the CRDs, so they
// must be applied before any InferencePool. The manifest is CRDs only and
// SHA-verified before apply (see version.GAIE*).
func EnsureGAIECRDs(ctx context.Context, r *Runner) error {
	body, err := downloadAndVerify(ctx, version.GAIEManifestURL, version.GAIEManifestSHA)
	if err != nil {
		return fmt.Errorf("GAIE %s CRDs: %w", version.GAIEVersion, err)
	}
	if err := r.Apply(ctx, string(body)); err != nil {
		return fmt.Errorf("apply GAIE %s CRDs: %w", version.GAIEVersion, err)
	}
	return r.Kubectl(ctx, "wait", "--for=condition=Established", "--timeout=60s",
		"crd/inferencepools.inference.networking.k8s.io")
}
