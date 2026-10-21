package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

func testCAPEM(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestApplyExtraCA(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "keys"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, b := testCAPEM(t, "a"), testCAPEM(t, "b")
	// No trailing newline on the first file: the bundle must still split.
	if err := os.WriteFile(filepath.Join(repo, "keys", "a.pem"), a[:len(a)-1], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "keys", "b.pem"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	k := &cluster.K3s{}
	p := &poc.PoC{}
	p.Cluster.ExtraCACerts = []string{"keys/a.pem", "keys/b.pem"}
	msg, err := applyExtraCA(k, p, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "2 cert(s) from 2 file(s)") {
		t.Errorf("summary = %q", msg)
	}
	if k.ExtraCAPath != filepath.Join(repo, "artifacts", "extra-ca.pem") {
		t.Errorf("ExtraCAPath = %q", k.ExtraCAPath)
	}
	data, err := os.ReadFile(k.ExtraCAPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := countPEMCerts(data); n != 2 {
		t.Errorf("bundle holds %d certs, want 2", n)
	}
}

func TestApplyExtraCAUnsetIsNoop(t *testing.T) {
	k := &cluster.K3s{}
	msg, err := applyExtraCA(k, &poc.PoC{}, t.TempDir())
	if err != nil || msg != "" || k.ExtraCAPath != "" {
		t.Errorf("unset extra_ca_certs should be a no-op: msg=%q err=%v path=%q", msg, err, k.ExtraCAPath)
	}
}

func TestApplyExtraCARejectsNonPEM(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "ca.pem"), []byte("not a cert\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &poc.PoC{}
	p.Cluster.ExtraCACerts = []string{"ca.pem"}
	if _, err := applyExtraCA(&cluster.K3s{}, p, repo); err == nil {
		t.Error("expected an error for a file with no PEM certificate")
	}
}
