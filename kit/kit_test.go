package kit

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderString(t *testing.T) {
	cases := []struct {
		name, src string
		vars      map[string]string
		want      string
		err       string
	}{
		{name: "scalar", src: "a: ${A}\n", vars: map[string]string{"A": "1"}, want: "a: 1\n"},
		{name: "empty value", src: "a: \"${A}\"\n", vars: map[string]string{"A": ""}, want: "a: \"\"\n"},
		{name: "value not rescanned", src: "j: ${J}\n", vars: map[string]string{"J": "${X}"}, want: "j: ${X}\n"},
		{name: "shell forms untouched", src: "x=${X:-d} $Y ${lower}\n", vars: nil, want: "x=${X:-d} $Y ${lower}\n"},
		{name: "block kept", src: "a\n#@if B\nb\n#@end\nc\n", vars: map[string]string{"B": "y"}, want: "a\nb\nc\n"},
		{name: "block dropped", src: "a\n  #@if B\nb ${MISSING}\n  #@end\nc\n", vars: map[string]string{"B": ""}, want: "a\nc\n"},
		{name: "nested", src: "#@if A\na\n#@if B\nb\n#@end\n#@end\n",
			vars: map[string]string{"A": "1", "B": ""}, want: "a\n"},
		{name: "no trailing newline", src: "a: ${A}", vars: map[string]string{"A": "1"}, want: "a: 1"},
		{name: "unsupplied var", src: "a: ${A}\n", err: "${A}: variable not supplied"},
		{name: "unsupplied guard", src: "#@if A\n#@end\n", err: "#@if A: variable not supplied"},
		{name: "stray end", src: "#@end\n", err: "#@end without #@if"},
		{name: "unclosed", src: "#@if A\n", vars: map[string]string{"A": "1"}, err: "not closed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderString(c.src, c.vars)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want containing %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

var refs = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}|#@if\s+([A-Z_][A-Z0-9_]*)`)

// templateVars lists every variable a template references.
func templateVars(src string) []string {
	seen := map[string]bool{}
	for _, m := range refs.FindAllStringSubmatch(src, -1) {
		seen[m[1]+m[2]] = true
	}
	var out []string
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// templates are the kit files the renderer is run on: platform/ manifests
// and scenario .tmpl files. Other scenario files are applied verbatim.
func templates(t *testing.T) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if (strings.HasPrefix(p, "platform/") && strings.HasSuffix(p, ".yaml")) || strings.HasSuffix(p, ".tmpl") {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Every template renders with all its variables set, and again with all
// empty, leaving no ${NAME} or marker behind.
func TestTemplatesRenderClean(t *testing.T) {
	for _, p := range templates(t) {
		src, _ := Read(p)
		for _, fill := range []string{"v", ""} {
			vars := map[string]string{}
			for _, v := range templateVars(src) {
				vars[v] = fill
			}
			out, err := Render(p, vars)
			if err != nil {
				t.Errorf("%s: %v", p, err)
				continue
			}
			if refs.MatchString(out) || strings.Contains(out, "#@end") {
				t.Errorf("%s (fill %q): unrendered reference left in output", p, fill)
			}
		}
	}
}

// awks returns every awk on PATH, so render.sh is checked against each
// flavour present (gawk, mawk, BSD/one-true awk, busybox).
func awks(t *testing.T) [][]string {
	t.Helper()
	var out [][]string
	for _, name := range []string{"awk", "gawk", "mawk", "nawk", "original-awk"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, []string{p})
		}
	}
	if bb, err := exec.LookPath("busybox"); err == nil {
		out = append(out, []string{bb, "awk"})
	}
	if len(out) == 0 {
		t.Skip("no awk on PATH")
	}
	return out
}

// runShell renders src with lib/render.sh under the given awk.
func runShell(t *testing.T, awk []string, src string, vars map[string]string) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "tmpl-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(src); err != nil {
		t.Fatal(err)
	}
	f.Close()
	awkCmd := awk[0]
	if len(awk) > 1 {
		// busybox needs its applet name; give render.sh a one-line wrapper.
		w := t.TempDir() + "/awk"
		if err := os.WriteFile(w, []byte("#!/bin/sh\nexec "+awk[0]+" "+awk[1]+" \"$@\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		awkCmd = w
	}
	cmd := exec.Command("sh", "lib/render.sh", f.Name())
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "AWK=" + awkCmd}
	for k, v := range vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if err != nil {
		return "", &shellErr{err, stderr.String()}
	}
	return stdout.String(), nil
}

type shellErr struct {
	err    error
	stderr string
}

func (e *shellErr) Error() string { return e.err.Error() + ": " + e.stderr }

// TestRenderShellParity: lib/render.sh and RenderString agree on every
// real kit template (all variables set, then all empty) and on the edge
// cases above, under every awk available.
func TestRenderShellParity(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	type tc struct {
		name, src string
		vars      map[string]string
	}
	var cases []tc
	for _, p := range templates(t) {
		src, _ := Read(p)
		for _, fill := range []string{"val-ue/with&special\\chars", ""} {
			vars := map[string]string{}
			for _, v := range templateVars(src) {
				vars[v] = fill
			}
			cases = append(cases, tc{p + " fill=" + fill, src, vars})
		}
	}
	cases = append(cases,
		tc{"nested", "#@if A\na ${A}\n  #@if B\nb\n  #@end\n#@end\nz\n", map[string]string{"A": "1", "B": ""}},
		tc{"shell forms", "x=${X:-d} $Y ${lower}\n", nil},
		tc{"crlf markers", "a\r\n#@if B\r\nb\r\n#@end\r\n", map[string]string{"B": "1"}},
	)
	for _, awk := range awks(t) {
		for _, c := range cases {
			want, err := RenderString(c.src, c.vars)
			if err != nil {
				t.Fatalf("%s: go: %v", c.name, err)
			}
			got, err := runShell(t, awk, c.src, c.vars)
			if err != nil {
				t.Errorf("%v %s: sh: %v", awk, c.name, err)
				continue
			}
			if got != want {
				t.Errorf("%v %s: render.sh differs from Go\n got: %q\nwant: %q", awk, c.name, got, want)
			}
		}
		// Unset variable is an error in both.
		if _, err := runShell(t, awk, "a: ${UNSET_VAR}\n", nil); err == nil {
			t.Errorf("%v: render.sh accepted an unset variable", awk)
		}
	}
}

// parseYAML decodes every document in src.
func parseYAML(src string) error {
	dec := yaml.NewDecoder(strings.NewReader(src))
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Every kit YAML file is valid YAML: templates once rendered, static
// files as they are (they are applied verbatim).
func TestKitYAMLParses(t *testing.T) {
	isTemplate := map[string]bool{}
	for _, p := range templates(t) {
		isTemplate[p] = true
	}
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yaml.tmpl") {
			return nil
		}
		src, _ := Read(p)
		if isTemplate[p] {
			vars := map[string]string{}
			for _, v := range templateVars(src) {
				vars[v] = "v"
			}
			if src, err = Render(p, vars); err != nil {
				return err
			}
		}
		if err := parseYAML(src); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// kit/README.md documents every platform file; adding one without a row
// there fails here.
func TestReadmeCoversPlatform(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	err = fs.WalkDir(FS, "platform", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if !strings.Contains(string(readme), "`"+name+"`") {
			t.Errorf("kit/README.md does not document %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
