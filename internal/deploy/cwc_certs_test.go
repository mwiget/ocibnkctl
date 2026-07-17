package deploy

import (
	"context"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/runtimeenv"
)

func TestCWCCertGen_InContainerSkipsHelperContainer(t *testing.T) {
	// In-container, both cert-gen steps must run tools in-process (the runner
	// image ships helm/make/openssl) rather than `docker run -v <local>` — that
	// bind mount is unreachable to the host daemon. This guards the branch:
	// with the override set, the failure must come from the in-process path,
	// never from a helper container.
	t.Setenv("OCIBNKCTL_IN_CONTAINER", "true")
	if !runtimeenv.InContainer() {
		t.Fatal("override not honored")
	}
	err := PullF5CertGen(context.Background(),
		OCIAuth{Username: "_json_key", Password: "x"}, "0.0.0-nonexistent", t.TempDir())
	if err == nil {
		t.Fatal("expected failure for a nonexistent chart version")
	}
	if strings.Contains(err.Error(), "docker run") || strings.Contains(err.Error(), "/work") {
		t.Fatalf("in-container path must not shell to a helper container; got: %v", err)
	}
}
