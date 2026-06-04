package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// setupKube returns a fresh PoC repo (with artifacts/) and a source
// kubeconfig path, and points HOME at a temp dir so ~/.kube/config
// resolves into the sandbox. It restores HOME on cleanup.
func setupKube(t *testing.T) (repo, src, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	repo = t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(repo, "artifacts", "kubeconfig")
	if err := os.WriteFile(src, []byte("KUBECONFIG-FROM-CLUSTER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo, src, home
}

func TestGlobalKubeconfig_CreatesWhenAbsent(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")

	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "KUBECONFIG-FROM-CLUSTER\n" {
		t.Fatalf("dst content = %q, err=%v", b, err)
	}
	if st, _ := readKubeState(repo); st.Action != "created" {
		t.Fatalf("action = %q, want created", st.Action)
	}

	removeGlobalKubeconfig(io.Discard, repo)
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("expected %s removed on destroy, stat err=%v", dst, err)
	}
}

func TestGlobalKubeconfig_OverwriteYesThenRestore(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("MY-ORIGINAL-CONFIG\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Stub the prompt → yes.
	orig := confirmKubeconfigOverwrite
	confirmKubeconfigOverwrite = func(io.Writer, string) bool { return true }
	defer func() { confirmKubeconfigOverwrite = orig }()

	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "KUBECONFIG-FROM-CLUSTER\n" {
		t.Fatalf("dst not overwritten: %q", b)
	}
	st, _ := readKubeState(repo)
	if st.Action != "overwrote" || st.Backup == "" {
		t.Fatalf("state = %+v, want overwrote + backup", st)
	}
	if b, _ := os.ReadFile(st.Backup); string(b) != "MY-ORIGINAL-CONFIG\n" {
		t.Fatalf("backup content = %q, want original", b)
	}

	// Destroy restores the original.
	removeGlobalKubeconfig(io.Discard, repo)
	if b, err := os.ReadFile(dst); err != nil || string(b) != "MY-ORIGINAL-CONFIG\n" {
		t.Fatalf("original not restored: %q err=%v", b, err)
	}
	if _, err := os.Stat(st.Backup); !os.IsNotExist(err) {
		t.Errorf("backup should be consumed by restore, stat err=%v", err)
	}
}

func TestGlobalKubeconfig_OverwriteNoLeavesUntouched(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("MY-ORIGINAL-CONFIG\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	orig := confirmKubeconfigOverwrite
	confirmKubeconfigOverwrite = func(io.Writer, string) bool { return false }
	defer func() { confirmKubeconfigOverwrite = orig }()

	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "MY-ORIGINAL-CONFIG\n" {
		t.Fatalf("config should be untouched, got %q", b)
	}
	if st, _ := readKubeState(repo); st.Action != "skipped" {
		t.Fatalf("action = %q, want skipped", st.Action)
	}
	// Destroy must NOT touch a config we declined to overwrite.
	removeGlobalKubeconfig(io.Discard, repo)
	if b, err := os.ReadFile(dst); err != nil || string(b) != "MY-ORIGINAL-CONFIG\n" {
		t.Fatalf("skipped config must survive destroy: %q err=%v", b, err)
	}
}

func TestGlobalKubeconfig_IdempotentRefresh(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")

	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	// Second run must NOT prompt and must NOT back up our own file.
	confirmKubeconfigOverwrite = func(io.Writer, string) bool {
		t.Fatal("second install must not prompt")
		return false
	}
	defer func() { confirmKubeconfigOverwrite = promptYesNo }()
	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	if st, _ := readKubeState(repo); st.Action != "created" || st.Backup != "" {
		t.Fatalf("state drifted on refresh: %+v", st)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("config missing after refresh: %v", err)
	}
}
