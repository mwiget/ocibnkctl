package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// setupKube returns a fresh PoC repo (with artifacts/) and a source
// kubeconfig path, and points HOME at a temp dir so ~/.kube/config
// resolves into the sandbox. HOME is restored on cleanup.
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

// --yolo authorizes overwriting a pre-existing config; the original is
// backed up and restored on destroy.
func TestGlobalKubeconfig_OverwriteBacksUpAndRestores(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("MY-ORIGINAL-CONFIG\n"), 0o600); err != nil {
		t.Fatal(err)
	}

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

	// Destroy restores the original and consumes the backup.
	removeGlobalKubeconfig(io.Discard, repo)
	if b, err := os.ReadFile(dst); err != nil || string(b) != "MY-ORIGINAL-CONFIG\n" {
		t.Fatalf("original not restored: %q err=%v", b, err)
	}
	if _, err := os.Stat(st.Backup); !os.IsNotExist(err) {
		t.Errorf("backup should be consumed by restore, stat err=%v", err)
	}
}

func TestGlobalKubeconfig_IdempotentRefresh(t *testing.T) {
	repo, src, home := setupKube(t)
	dst := filepath.Join(home, ".kube", "config")

	if err := installGlobalKubeconfig(io.Discard, repo, src); err != nil {
		t.Fatal(err)
	}
	// Second run must refresh in place without backing up our own file.
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
