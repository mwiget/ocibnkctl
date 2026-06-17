package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

// SelectedBackend reports the cluster backend. ocibnkctl ships a single
// native-k3s backend; kept as a function so call sites read
// intentionally and an alternative backend stays a one-line change.
func SelectedBackend() cluster.Backend {
	return cluster.BackendK3s
}

// invocationName is the binary's own basename, used in help text and
// "next:" hints.
func invocationName() string {
	base := filepath.Base(os.Args[0])
	if base == "." || base == "/" || base == "" {
		return "ocibnkctl"
	}
	return base
}

// newProvisioner builds the Provisioner for the selected backend.
func newProvisioner(rt cluster.Runtime, out io.Writer) (cluster.Provisioner, error) {
	return cluster.NewProvisioner(SelectedBackend(), rt, out)
}

// applyRegistryCache wires the provisioner's node containers to the local
// pull-through cache fleet when poc.yaml's cluster.registry_cache.enabled is
// set: it renders registries.yaml into <repo>/artifacts, sets the path on the
// k3s backend (so every node bind-mounts it), and returns a one-line summary
// for the caller to log. A no-op (returns "") when the cache is disabled or the
// backend is not k3s. Must be called BEFORE CreateCluster / AddWorker.
func applyRegistryCache(prov cluster.Provisioner, p *poc.PoC, repo string) (string, error) {
	rc := p.Cluster.RegistryCache
	if !rc.Enabled {
		return "", nil
	}
	k3sProv, ok := prov.(*cluster.K3s)
	if !ok {
		return "", nil
	}
	// The repo.f5.com cache is credential-free — the CLUSTER supplies its own
	// FAR key (so GA vs engineering builds can use different keys against one
	// cache). Pull the creds from THIS PoC's far_key_ref into the client-side
	// registries.yaml configs; the cache never sees a stored secret. Best-
	// effort: if the key is absent, the public mirrors still work and the F5
	// mirror falls back to the direct upstream (with the in-cluster far-secret).
	var f5User, f5Pass string
	credNote := "no FAR key → F5 pulls fall back to direct"
	if ref := p.BNK.FARKeyRef; ref != "" {
		keyPath := ref
		if !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(repo, ref)
		}
		if u, pw, err := deploy.FARCreds(keyPath); err == nil {
			f5User, f5Pass = u, pw
			credNote = "F5 creds → registries.yaml configs (cache holds none)"
		} else {
			credNote = fmt.Sprintf("FAR key unreadable (%v) → F5 pulls fall back to direct", err)
		}
	}
	content := cluster.RenderRegistriesYAML(rc.CacheHost(), rc.CachePortBase(), f5User, f5Pass)
	if err := os.MkdirAll(filepath.Join(repo, "artifacts"), 0o755); err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(repo, "artifacts", "registries.yaml"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	k3sProv.RegistriesYAMLPath = path
	return fmt.Sprintf("registry cache → %s:%d.. (%s)", rc.CacheHost(), rc.CachePortBase(), credNote), nil
}
