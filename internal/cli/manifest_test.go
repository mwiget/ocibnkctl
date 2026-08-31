package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// newProbedPoC scaffolds a real PoC via `init` so resolveFARPath is
// exercised against the same poc.yaml shape operators actually get.
func newProbedPoC(t *testing.T) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "demo")
	if out, err := runInit(t, target); err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	return target
}

func TestResolveFARPath_FromPoC(t *testing.T) {
	target := newProbedPoC(t)

	got, err := resolveFARPath(target, "")
	if err != nil {
		t.Fatalf("resolveFARPath: %v", err)
	}
	want := filepath.Join(target, "keys", "f5-far-auth-key.tgz")
	if got != want {
		t.Errorf("far path = %q, want %q", got, want)
	}
}

// --far must work with no PoC anywhere in sight — that's what makes the
// probe usable from a bare checkout when deciding whether to bump the pin.
func TestResolveFARPath_FlagWinsAndNeedsNoPoC(t *testing.T) {
	dir := t.TempDir()
	far := filepath.Join(dir, "somewhere", "far.tgz")
	if err := os.MkdirAll(filepath.Dir(far), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(far, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// dir is deliberately NOT a PoC.
	got, err := resolveFARPath(dir, far)
	if err != nil {
		t.Fatalf("resolveFARPath with --far: %v", err)
	}
	if got != far {
		t.Errorf("far path = %q, want %q", got, far)
	}

	// And it still wins when a PoC is present.
	target := newProbedPoC(t)
	got, err = resolveFARPath(target, far)
	if err != nil {
		t.Fatalf("resolveFARPath with --far over a PoC: %v", err)
	}
	if got != far {
		t.Errorf("--far did not override the PoC ref: got %q", got)
	}
}

func TestResolveFARPath_NoPoCNoFlagExplainsItself(t *testing.T) {
	_, err := resolveFARPath(t.TempDir(), "")
	if err == nil {
		t.Fatal("expected an error outside a PoC with no --far")
	}
	if !strings.Contains(err.Error(), "--far") {
		t.Errorf("error should point at --far, got: %v", err)
	}
}

// A missing FAR tgz must fail before any registry traffic, with a message
// that names the path it looked at.
func TestManifestProbe_MissingFARKeyFailsEarly(t *testing.T) {
	target := newProbedPoC(t)
	far := filepath.Join(target, "keys", "f5-far-auth-key.tgz")

	var out strings.Builder
	err := runManifestProbe(context.Background(), &out, version.CNEManifestVersion,
		&manifestProbeFlags{pocDir: target})
	if err == nil {
		t.Fatal("expected an error when the FAR tgz is absent")
	}
	if !strings.Contains(err.Error(), far) {
		t.Errorf("error should name the missing path %q, got: %v", far, err)
	}
}

// The default (no positional arg) must be the pin this binary carries —
// otherwise `manifest probe` silently checks the wrong thing.
func TestManifestProbeCmd_DefaultsToPinnedVersion(t *testing.T) {
	cmd := newManifestProbeCmd()
	if !strings.Contains(cmd.Long, version.CNEManifestVersion) {
		t.Errorf("help text does not name the pinned manifest version %s", version.CNEManifestVersion)
	}
	if err := cmd.Args(cmd, []string{"a", "b"}); err == nil {
		t.Error("probe should reject more than one version argument")
	}
}

func TestManifestCmd_WiredIntoRoot(t *testing.T) {
	root := NewRootCmd()
	var manifest bool
	for _, c := range root.Commands() {
		if c.Name() == "manifest" {
			manifest = true
			for _, sub := range c.Commands() {
				if sub.Name() == "probe" {
					return
				}
			}
			t.Fatal("manifest command has no `probe` subcommand")
		}
	}
	if !manifest {
		t.Fatal("root command tree is missing `manifest`")
	}
}
