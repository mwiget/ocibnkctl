package scenarios

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mwiget/ocibnkctl/kit"
)

// A shell scenario lives entirely in kit/scenarios/<name>/: scenario.yaml
// (metadata), scenario.env (variables), apply.sh / verify.sh / cleanup.sh
// and manifests/. The scripts run with plain kubectl, so the same scenario
// works from `ocibnkctl scenario run` and by hand on any cluster; this
// adapter only feeds them the kubeconfig and edge settings and reads
// verify.sh's JSON lines into a Result. See kit/lib/common.sh for the
// script contract and docs/agentic-kit-plan.md (Phase 2) for the why.

type shellMeta struct {
	Name         string   `yaml:"name"`
	Title        string   `yaml:"title"`
	Rating       Rating   `yaml:"rating"`
	Dependencies []string `yaml:"dependencies"`
	Description  string   `yaml:"description"`
}

type shellScenario struct{ meta shellMeta }

func init() {
	for _, s := range loadShellScenarios() {
		Register(s)
	}
}

// loadShellScenarios returns a scenario for every kit/scenarios/<name>/
// that has a verify.sh. A malformed scenario.yaml is a build-time bug in
// the kit, so it panics (TestShellScenariosLoad catches it).
func loadShellScenarios() []*shellScenario {
	entries, err := fs.ReadDir(kit.FS, "scenarios")
	if err != nil {
		panic("scenarios: read kit/scenarios: " + err.Error())
	}
	var out []*shellScenario
	for _, e := range entries {
		dir := "scenarios/" + e.Name()
		if _, err := fs.Stat(kit.FS, dir+"/verify.sh"); err != nil {
			continue
		}
		raw, err := kit.FS.ReadFile(dir + "/scenario.yaml")
		if err != nil {
			panic("scenarios: " + dir + ": " + err.Error())
		}
		var m shellMeta
		if err := yaml.Unmarshal(raw, &m); err != nil {
			panic("scenarios: " + dir + "/scenario.yaml: " + err.Error())
		}
		if m.Name != e.Name() {
			panic(fmt.Sprintf("scenarios: %s/scenario.yaml names %q", dir, m.Name))
		}
		out = append(out, &shellScenario{meta: m})
	}
	return out
}

func (s *shellScenario) Name() string           { return s.meta.Name }
func (s *shellScenario) Title() string          { return s.meta.Title }
func (s *shellScenario) Rating() Rating         { return s.meta.Rating }
func (s *shellScenario) Dependencies() []string { return s.meta.Dependencies }
func (s *shellScenario) Description() string    { return strings.TrimSpace(s.meta.Description) }

// KitDir is the scenario's directory inside kit.FS. Its presence marks a
// scenario whose scripts and manifests can be copied out whole.
func (s *shellScenario) KitDir() string { return "scenarios/" + s.meta.Name }

// kitRoot is where Manifests writes the kit tree the scripts run from:
// <PoC>/artifacts/kit/{lib,scenarios/<name>}. The operator can rerun the
// same scripts from there by hand.
func kitRoot(pocDir string) string { return filepath.Join(pocDir, "artifacts", "kit") }

// Manifests writes kit/lib and this scenario's kit directory under
// artifacts/kit/, and copies the manifests to artifacts/scenarios/<name>/
// like every other scenario. Returns the manifest copies.
func (s *shellScenario) Manifests(ctx *Context) ([]string, error) {
	root := kitRoot(ctx.PoCDir)
	for _, dir := range []string{"lib", s.KitDir()} {
		if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
			return nil, err
		}
		if err := extractKit(dir, root); err != nil {
			return nil, err
		}
	}
	var paths []string
	err := fs.WalkDir(kit.FS, s.KitDir()+"/manifests", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := kit.FS.ReadFile(p)
		if err != nil {
			return err
		}
		out, err := WriteManifest(ctx.PoCDir, s.Name(), filepath.Base(p), string(body))
		paths = append(paths, out)
		return err
	})
	return paths, err
}

// extractKit copies kit.FS's dir (recursively) to root/dir; *.sh files are
// made executable.
func extractKit(dir, root string) error {
	return fs.WalkDir(kit.FS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		body, err := kit.FS.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") && !strings.Contains(p, "/manifests/") {
			mode = 0o755 // scripts run by hand; .sh payloads under manifests/ are data
		}
		return os.WriteFile(dst, body, mode)
	})
}

func (s *shellScenario) Apply(ctx *Context) error {
	return s.run(ctx, "apply.sh", logWriter(ctx))
}

func (s *shellScenario) Cleanup(ctx *Context) error {
	if _, err := s.Manifests(ctx); err != nil { // clean may run without a prior run
		return err
	}
	return s.run(ctx, "cleanup.sh", logWriter(ctx))
}

func (s *shellScenario) Verify(ctx *Context) Result {
	var stdout bytes.Buffer
	err := s.run(ctx, "verify.sh", &stdout)
	res := parseVerify(&stdout, logWriter(ctx))
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		// 1 = some check failed; the assertions say which.
	default:
		res.Assertions = append(res.Assertions, Assertion{
			Description: "verify.sh completed",
			Got:         oneLineShell(err.Error()),
		})
	}
	return finalizeShell(res)
}

// run executes kit/scenarios/<name>/<script> from the extracted kit tree,
// with stdout to out and stderr to the scenario log.
func (s *shellScenario) run(ctx *Context, script string, out io.Writer) error {
	path := filepath.Join(kitRoot(ctx.PoCDir), filepath.FromSlash(s.KitDir()), script)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%s: %w (Manifests not run?)", script, err)
	}
	cmd := exec.CommandContext(ctx.Ctx, "sh", path)
	cmd.Env = shellEnv(ctx)
	cmd.Stdout = out
	cmd.Stderr = logWriter(ctx)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", script, err)
	}
	return nil
}

// shellEnv is the caller's environment plus the cluster's kubeconfig and,
// unless the operator set EDGE_DRIVER themselves, the ocibnkctl edge
// fabric (`cluster up`'s external FRR container) as the data-plane client.
func shellEnv(ctx *Context) []string {
	env := os.Environ()
	if ctx.Runner != nil && ctx.Runner.KubeconfigPath != "" {
		env = append(env, "KUBECONFIG="+ctx.Runner.KubeconfigPath)
	}
	if os.Getenv("EDGE_DRIVER") == "" && ctx.PoC != nil {
		env = append(env,
			"EDGE_DRIVER=ocibnkctl",
			"EDGE_RUNTIME="+runtimeBin(ctx),
			"EDGE_FRR="+EdgeFRRName(ctx),
			"EDGE_PREFIX="+EdgePrefix(ctx),
			"EDGE_TOOLS_IMAGE="+edgeToolsImage,
		)
	}
	return env
}

// verifyLine is one JSON line from verify.sh: an assertion or the summary.
type verifyLine struct {
	Description *string `json:"description"`
	OK          bool    `json:"ok"`
	Got         string  `json:"got"`
	Skipped     bool    `json:"skipped"`
	Summary     *string `json:"summary"`
}

// parseVerify reads verify.sh's stdout: JSON assertion and summary lines
// become the Result; any other line is passed through to log.
func parseVerify(r io.Reader, log io.Writer) Result {
	var res Result
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		var v verifyLine
		if !strings.HasPrefix(strings.TrimSpace(line), "{") || json.Unmarshal([]byte(line), &v) != nil {
			fmt.Fprintln(log, line)
			continue
		}
		switch {
		case v.Description != nil:
			res.Assertions = append(res.Assertions, Assertion{
				Description: *v.Description, OK: v.OK, Got: v.Got, Skipped: v.Skipped,
			})
		case v.Summary != nil:
			res.Summary = *v.Summary
		default:
			fmt.Fprintln(log, line)
		}
	}
	return res
}

// finalizeShell sets Status from the assertions: ok when all passed (the
// script's summary stands), else failed with the failed checks listed.
func finalizeShell(res Result) Result {
	if len(res.Assertions) > 0 && res.AllPassed() {
		res.Status = "ok"
		if res.Summary == "" {
			res.Summary = fmt.Sprintf("%d check(s) passed", len(res.Assertions))
		}
		return res
	}
	res.Status = "failed"
	var failed []string
	for _, a := range res.Assertions {
		if !a.OK {
			failed = append(failed, a.Description)
		}
	}
	if len(failed) == 0 {
		failed = []string{"verify.sh reported no checks"}
	}
	res.Summary = "failed: " + strings.Join(failed, "; ")
	return res
}

func oneLineShell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// logWriter prefixes each line of script output like the Runner's kubectl
// output, so scenario logs read the same either way.
func logWriter(ctx *Context) io.Writer { return &linePrefixer{w: ctx.Out, prefix: "      | "} }

type linePrefixer struct {
	w      io.Writer
	prefix string
	mid    bool
}

func (l *linePrefixer) Write(b []byte) (int, error) {
	for _, line := range bytes.SplitAfter(b, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if !l.mid {
			if _, err := io.WriteString(l.w, l.prefix); err != nil {
				return 0, err
			}
		}
		if _, err := l.w.Write(line); err != nil {
			return 0, err
		}
		l.mid = line[len(line)-1] != '\n'
	}
	return len(b), nil
}
