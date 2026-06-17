package poc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegistryCacheStrictDecode ensures a poc.yaml carrying the opt-in
// registry_cache block decodes under strict (KnownFields) Load, and that the
// defaults resolve as documented.
func TestRegistryCacheStrictDecode(t *testing.T) {
	dir := t.TempDir()
	const y = `
metadata:
  name: rc
cluster:
  name: rc
  provider: docker
  registry_cache:
    enabled: true
`
	if err := os.WriteFile(filepath.Join(dir, "poc.yaml"), []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load with registry_cache failed: %v", err)
	}
	rc := p.Cluster.RegistryCache
	if !rc.Enabled {
		t.Fatal("registry_cache.enabled did not decode")
	}
	if rc.CacheHost() != "host.docker.internal" {
		t.Errorf("CacheHost default = %q", rc.CacheHost())
	}
	if rc.CachePortBase() != 5000 {
		t.Errorf("CachePortBase default = %d", rc.CachePortBase())
	}
}

func TestRegistryCacheDefaultsDisabled(t *testing.T) {
	if (RegistryCache{}).Enabled {
		t.Fatal("registry_cache must be opt-in (disabled by default)")
	}
}
