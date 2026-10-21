package poc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtraCACertsStrictDecode ensures a poc.yaml carrying cluster.extra_ca_certs
// decodes under strict (KnownFields) Load.
func TestExtraCACertsStrictDecode(t *testing.T) {
	dir := t.TempDir()
	const y = `
metadata:
  name: ca
cluster:
  name: ca
  provider: docker
  extra_ca_certs:
    - keys/netskope-ca.pem
`
	if err := os.WriteFile(filepath.Join(dir, "poc.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load with extra_ca_certs failed: %v", err)
	}
	if got := p.Cluster.ExtraCACerts; len(got) != 1 || got[0] != "keys/netskope-ca.pem" {
		t.Errorf("extra_ca_certs = %v", got)
	}
}
