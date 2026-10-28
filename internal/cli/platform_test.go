package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

const kitDir = "../../kit"

// envMap turns KEY=value lines into a map.
func envMap(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}
	return m
}

// shellDefaults sources kit/platform/platform.env in an empty environment
// and returns what it sets, as sh itself resolves it.
func shellDefaults(t *testing.T) map[string]string {
	t.Helper()
	cmd := exec.Command("sh", "-c", `set -a; . "$1"; set +a; env`, "sh", filepath.Join(kitDir, "platform", "platform.env"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return envMap(strings.Split(string(out), "\n"))
}

// platform.env's defaults are what ocibnkctl passes for a default PoC, so a
// hand run and `ocibnkctl deploy` install the same thing. Paths and the
// helm/kit state locations are PoC-specific and skipped.
func TestPlatformEnvMatchesGo(t *testing.T) {
	defaults := shellDefaults(t)
	goEnv := envMap(platformEnv("/poc", poc.New("x"), "/poc/artifacts/kubeconfig"))
	skip := map[string]bool{"FAR_KEY": true, "JWT_FILE": true, "NODE_EXEC": true}
	for k, v := range goEnv {
		if skip[k] || strings.HasPrefix(k, "KIT_") || strings.HasPrefix(k, "HELM_") || k == "KUBECONFIG" {
			continue
		}
		d, ok := defaults[k]
		switch {
		case !ok:
			t.Errorf("ocibnkctl passes %s but kit/platform/platform.env has no default for it", k)
		case d != v:
			t.Errorf("%s: platform.env default %q, ocibnkctl passes %q", k, d, v)
		}
	}
}

// The scripts render the CNEInstance from DATAPLANE_MODE and the rest of the
// environment (kit/platform/lib.sh cne_vars); `ocibnkctl render` uses the
// Go renderer. Both must agree for every PoC shape the golden files cover.
func TestPlatformCNEMatchesGo(t *testing.T) {
	lib, _ := filepath.Abs(filepath.Join(kitDir, "lib"))
	for name, mutate := range goldenCases {
		t.Run(name, func(t *testing.T) {
			p := poc.New("golden")
			mutate(p)
			want, err := deploy.RenderCNEInstance(p)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", `kit_die() { echo "$*" >&2; exit 2; }
. "$KIT_LIB/../platform/platform.env"; . "$KIT_LIB/../platform/lib.sh"; render_cne`)
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "KIT_LIB=" + lib},
				platformEnv("/poc", p, "/kubeconfig")...)
			got, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("render_cne: %v\n%s", err, got)
			}
			if string(got) != want {
				t.Errorf("shell CNEInstance differs from Go:\n%s", firstDiff(want, string(got)))
			}
		})
	}
}
