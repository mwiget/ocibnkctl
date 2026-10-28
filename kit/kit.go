// Package kit is the plain-file source of truth for every manifest
// ocibnkctl applies (docs/agentic-kit-plan.md). The files under kit/ are
// meant to be read and used directly: by hand, by an agent following
// AGENTS.md, or by shell scripts. The binary embeds the same bytes, so
// both paths apply identical manifests.
//
// Template files use two constructs, nothing else:
//
//	${NAME}       replaced by the value of NAME
//	#@if NAME     on a line of its own: keep the lines up to the matching
//	#@end         #@end only when NAME is non-empty (blocks nest)
//
// NAME is upper-case letters, digits and underscores. Every NAME a
// template uses must be supplied (possibly empty): a missing one is an
// error, never a silent blank. Anything else, including $NAME without
// braces and shell forms like ${NAME:-default}, passes through untouched.
// Marker lines are removed from the output. lib/render.sh is the
// equivalent for the shell; TestRenderShellParity keeps the two in step.
package kit

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FS holds the kit tree: platform/ (the BNK install), scenarios/, lib/ and
// README.md.
//
//go:embed all:platform all:scenarios lib README.md
var FS embed.FS

// Tree is what makes up a usable kit on disk (ExtractAll, the release
// tarball): everything in FS.
var Tree = []string{"README.md", "lib", "platform", "scenarios"}

var (
	varRef   = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)
	ifMarker = regexp.MustCompile(`^\s*#@if\s+([A-Z_][A-Z0-9_]*)\s*$`)
	endMark  = regexp.MustCompile(`^\s*#@end\s*$`)
)

// Render reads the kit file at path (e.g. "platform/cne/cne-instance.yaml")
// and renders it with vars.
func Render(path string, vars map[string]string) (string, error) {
	raw, err := FS.ReadFile(path)
	if err != nil {
		return "", err
	}
	out, err := RenderString(string(raw), vars)
	if err != nil {
		return "", fmt.Errorf("render %s: %w", path, err)
	}
	return out, nil
}

// RenderString renders template src with vars; see the package comment.
func RenderString(src string, vars map[string]string) (string, error) {
	var out strings.Builder
	var stack []bool // per open #@if: is that block kept?
	keep := func() bool {
		for _, k := range stack {
			if !k {
				return false
			}
		}
		return true
	}
	lines := strings.SplitAfter(src, "\n")
	for i, line := range lines {
		bare := strings.TrimRight(line, "\r\n")
		if m := ifMarker.FindStringSubmatch(bare); m != nil {
			v, ok := vars[m[1]]
			if !ok {
				return "", fmt.Errorf("line %d: #@if %s: variable not supplied", i+1, m[1])
			}
			stack = append(stack, v != "")
			continue
		}
		if endMark.MatchString(bare) {
			if len(stack) == 0 {
				return "", fmt.Errorf("line %d: #@end without #@if", i+1)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if !keep() {
			continue
		}
		var missing string
		line = varRef.ReplaceAllStringFunc(line, func(ref string) string {
			name := ref[2 : len(ref)-1]
			v, ok := vars[name]
			if !ok && missing == "" {
				missing = name
			}
			return v
		})
		if missing != "" {
			return "", fmt.Errorf("line %d: ${%s}: variable not supplied", i+1, missing)
		}
		out.WriteString(line)
	}
	if len(stack) != 0 {
		return "", fmt.Errorf("%d #@if block(s) not closed by #@end", len(stack))
	}
	return out.String(), nil
}

// Read returns a kit file verbatim (for static manifests and data files).
func Read(path string) (string, error) {
	b, err := FS.ReadFile(path)
	return string(b), err
}

// ScenarioDir is one scenario's directory under scenarios/, holding its
// manifests/. Paths are relative to it ("manifests/01-namespace.yaml").
type ScenarioDir struct{ fs.FS }

// ReadFile reads a file relative to the scenario directory.
func (d ScenarioDir) ReadFile(name string) ([]byte, error) { return fs.ReadFile(d.FS, name) }

// Scenario returns the kit directory of the scenario with the given name.
func Scenario(name string) ScenarioDir {
	sub, err := fs.Sub(FS, "scenarios/"+name)
	if err != nil {
		panic("kit: " + err.Error()) // only on an invalid path pattern
	}
	return ScenarioDir{sub}
}

// Extract writes the kit directory dir (e.g. "lib", "platform",
// "scenarios/<name>") to root/dir, replacing what is there, so its scripts
// can run from disk. Shell scripts are executable, except .sh payloads under
// a manifests/ directory (data shipped in a ConfigMap).
func Extract(dir, root string) error {
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
		return err
	}
	return fs.WalkDir(FS, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		body, err := FS.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") && !strings.Contains(p, "/manifests/") {
			mode = 0o755
		}
		return os.WriteFile(dst, body, mode)
	})
}

// ExtractAll writes the whole kit (Tree) to root, replacing what is there.
func ExtractAll(root string) error {
	for _, dir := range Tree {
		if err := Extract(dir, root); err != nil {
			return err
		}
	}
	return nil
}
