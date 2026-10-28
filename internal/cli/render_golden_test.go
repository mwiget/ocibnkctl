package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// updateGolden rewrites testdata/render/ from the current code:
//
//	go test ./internal/cli -run TestRenderGolden -update
//
// The golden files pin render's output byte for byte. They were captured
// before the manifests moved out of Go into kit/ (docs/agentic-kit-plan.md,
// Phase 1), so that move must leave them unchanged; any later diff should be
// a deliberate manifest change, reviewed as such.
var updateGolden = flag.Bool("update", false, "rewrite testdata/render golden files")

// goldenCases cover every structural branch of the rendered manifests:
// each dataplane mode, multi-worker, small host, AI gateway with and
// without a console URL, and a non-default deployment size and edge octet.
var goldenCases = map[string]func(p *poc.PoC){
	"anycast-bgp": func(p *poc.PoC) { p.BNK.TMMDataplaneMode = poc.DataplaneAnycastBGP },
	"selfip-dag-3w": func(p *poc.PoC) {
		p.BNK.TMMDataplaneMode = poc.DataplaneSelfIPDAG
		p.Cluster.TMMNodes = 3
	},
	"standby-small": func(p *poc.PoC) {
		p.BNK.TMMDataplaneMode = poc.DataplaneStandby
		p.BNK.HostProfile = poc.HostProfileSmall
	},
	"aigw-console": func(p *poc.PoC) {
		p.Cluster.EdgeOctet = 42
		p.BNK.DeploymentSize = "Medium"
		p.BNK.AIGateway = poc.AIGateway{Enabled: true, AIMetrics: true, ConsoleURL: "http://console.example:3000"}
	},
	"aigw-plain": func(p *poc.PoC) {
		p.BNK.AIGateway = poc.AIGateway{Enabled: true}
	},
}

// goldenScenarioCases also pin the scenarios/ tree.
var goldenScenarioCases = map[string]bool{"anycast-bgp": true, "selfip-dag-3w": true}

func TestRenderGolden(t *testing.T) {
	for name, mutate := range goldenCases {
		t.Run(name, func(t *testing.T) {
			p := poc.New("golden")
			mutate(p)
			all, err := renderBundle(t.TempDir(), p, false)
			if err != nil {
				t.Fatal(err)
			}
			// Scenario manifests only vary with the worker count and edge
			// octet; pin them in two cases rather than five.
			var files []renderedFile
			for _, f := range all {
				if strings.HasPrefix(f.Path, "scenarios/") && !goldenScenarioCases[name] {
					continue
				}
				files = append(files, f)
			}
			dir := filepath.Join("testdata", "render", name)
			if *updateGolden {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				for _, f := range files {
					path := filepath.Join(dir, f.Path)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(f.Body), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			got := map[string]string{}
			for _, f := range files {
				got[f.Path] = f.Body
			}
			want := readTree(t, dir)
			for path, body := range want {
				g, ok := got[path]
				switch {
				case !ok:
					t.Errorf("%s: no longer rendered", path)
				case g != body:
					t.Errorf("%s differs from golden:\n%s", path, firstDiff(body, g))
				}
			}
			for path := range got {
				if _, ok := want[path]; !ok {
					t.Errorf("%s: new file not in golden (rerun with -update if intended)", path)
				}
			}
		})
	}
}

func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", dir, err)
	}
	return out
}

// firstDiff reports the first differing line, with its number.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return "  line " + strconv.Itoa(i+1) + "\n  want: " + wl + "\n  got:  " + gl
		}
	}
	return "  (identical lines, differing length)"
}
