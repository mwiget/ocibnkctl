package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// runInit drives the real init command against a temp target.
func runInit(t *testing.T, target string) (string, error) {
	t.Helper()
	cmd := newInitCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"demo", "--dir", target, "--no-git"})
	err := cmd.Execute()
	return out.String(), err
}

// BNK Forge materializes declared secret_files into <poc>/keys/ before it runs
// the init step, so the target directory always pre-exists there. Init must
// adopt it rather than refuse, or the container module can never deploy.
func TestInit_AdoptsPreSeededDirectory(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo")
	keys := filepath.Join(target, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(keys, "f5-far-auth-key.tgz")
	if err := os.WriteFile(secret, []byte("far"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runInit(t, target)
	if err != nil {
		t.Fatalf("init refused a pre-seeded directory: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(target, poc.FileName)); err != nil {
		t.Errorf("%s not written: %v", poc.FileName, err)
	}
	// The pre-seeded secret must survive init.
	body, err := os.ReadFile(secret)
	if err != nil || string(body) != "far" {
		t.Errorf("pre-seeded secret clobbered: body=%q err=%v", body, err)
	}
}

func TestInit_RefusesDirectoryHoldingAPoC(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo")
	if _, err := runInit(t, target); err != nil {
		t.Fatalf("first init failed: %v", err)
	}

	out, err := runInit(t, target)
	if err == nil {
		t.Fatalf("second init must refuse an initialized PoC\n%s", out)
	}
	if !strings.Contains(err.Error(), poc.FileName) {
		t.Errorf("error should name %s, got: %v", poc.FileName, err)
	}
}

func TestInit_RefusesNonDirectoryTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo")
	if err := os.WriteFile(target, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runInit(t, target); err == nil {
		t.Fatal("init must refuse a target that is a plain file")
	}
}
