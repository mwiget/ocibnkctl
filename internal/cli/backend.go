package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/mwiget/ocibnkctl/internal/cluster"
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
