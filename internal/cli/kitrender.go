package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/kit"
)

// kitRenderer runs the platform's render_* functions (kit/platform/lib.sh)
// for one PoC, so `render` writes exactly what the install scripts apply.
type kitRenderer struct {
	root string
	env  []string
}

// newKitRenderer extracts kit/{lib,platform} to a temp dir; call close when
// done.
func newKitRenderer(repo string, p *poc.PoC) (*kitRenderer, error) {
	root, err := os.MkdirTemp("", "ocibnkctl-kit-*")
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{"lib", "platform"} {
		if err := kit.Extract(dir, root); err != nil {
			os.RemoveAll(root)
			return nil, fmt.Errorf("extract kit/%s: %w", dir, err)
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"KIT_LIB=" + filepath.Join(root, "lib"), "KIT_SCENARIO_DIR=" + filepath.Join(root, "platform")}
	return &kitRenderer{root: root, env: append(env, platformEnv(repo, p, "")...)}, nil
}

func (k *kitRenderer) close() { os.RemoveAll(k.root) }

// run evaluates script after sourcing the platform helpers; extra adds
// KEY=value pairs to its environment. Returns stdout.
func (k *kitRenderer) run(script string, extra ...string) (string, error) {
	cmd := exec.Command("sh", "-c", `. "$KIT_LIB/common.sh"; . "$KIT_SCENARIO_DIR/platform.env"; . "$KIT_SCENARIO_DIR/lib.sh"; `+script)
	cmd.Env = append(append([]string{}, k.env...), extra...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kit %s: %v: %s", script, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// manifest is run for a render_* function whose output is a manifest:
// it must not be empty.
func (k *kitRenderer) manifest(fn string, extra ...string) (string, error) {
	s, err := k.run(fn, extra...)
	if err == nil && strings.TrimSpace(s) == "" {
		err = fmt.Errorf("kit %s rendered nothing", fn)
	}
	return s, err
}
