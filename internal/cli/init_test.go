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

func runInitArgs(t *testing.T, target string, extra ...string) (string, error) {
	t.Helper()
	cmd := newInitCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"demo", "--dir", target, "--no-git"}, extra...))
	err := cmd.Execute()
	return out.String(), err
}

func TestInit_ProfileLLMGateway(t *testing.T) {
	target := filepath.Join(t.TempDir(), "demo")
	out, err := runInitArgs(t, target, "--profile", "llm-gateway")
	if err != nil {
		t.Fatalf("init --profile llm-gateway: %v\n%s", err, out)
	}
	p, err := poc.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if gw := p.BNK.AIGateway; !gw.Enabled || !gw.AIMetrics {
		t.Errorf("ai_gateway = %+v, want enabled with metrics", gw)
	}
	if !p.BNK.IsAnycastBGP() {
		t.Errorf("data plane = %q, want anycast-bgp (clients reach the VIP over BGP)", p.BNK.DataplaneMode())
	}
	if strings.Join(p.Scenarios, ",") != "bgp-peer-frr,llm-gateway" {
		t.Errorf("scenarios = %v", p.Scenarios)
	}
	if !strings.Contains(out, "--with-scenarios") {
		t.Errorf("Next: should point at e2e --with-scenarios:\n%s", out)
	}
	if r := p.Validate(); !r.Valid() {
		t.Errorf("profile PoC must validate: %v", r.Errors)
	}
	// the profile's scripts, executable, and the hint pointing at them
	for _, f := range []string{"ask.sh", "console.sh", "demo-password.sh", "admin-password.sh"} {
		st, err := os.Stat(filepath.Join(target, f))
		if err != nil {
			t.Fatalf("%s not copied: %v", f, err)
		}
		if st.Mode().Perm()&0o100 == 0 {
			t.Errorf("%s mode = %v, want executable", f, st.Mode().Perm())
		}
	}
	if !strings.Contains(out, "./ask.sh") {
		t.Errorf("Next: should mention ./ask.sh:\n%s", out)
	}
}

func TestInit_NoProfileNoAskScript(t *testing.T) {
	target := filepath.Join(t.TempDir(), "plain")
	if out, err := runInitArgs(t, target); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(target, "ask.sh")); err == nil {
		t.Error("ask.sh copied without --profile llm-gateway")
	}
}

func TestInit_ProfileFromEnvAndUnknown(t *testing.T) {
	t.Setenv(envProfile, "llm-gateway")
	target := filepath.Join(t.TempDir(), "demo")
	if out, err := runInitArgs(t, target); err != nil {
		t.Fatalf("init with %s: %v\n%s", envProfile, err, out)
	}
	if p, _ := poc.Load(target); !p.BNK.AIGateway.Enabled {
		t.Errorf("%s=llm-gateway did not enable the AI gateway", envProfile)
	}

	out, err := runInitArgs(t, filepath.Join(t.TempDir(), "demo"), "--profile", "nope")
	if err == nil || !strings.Contains(err.Error(), "llm-gateway") {
		t.Errorf("unknown profile must fail and list the valid ones; err=%v\n%s", err, out)
	}
}
