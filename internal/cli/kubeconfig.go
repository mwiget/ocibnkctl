package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// globalKubeState records what `cluster up` did to ~/.kube/config so
// `destroy` can undo it precisely. Stored in the PoC's artifacts/.
type globalKubeState struct {
	Path   string `json:"path"`             // the ~/.kube/config we wrote
	Action string `json:"action"`           // created | overwrote | skipped
	Backup string `json:"backup,omitempty"` // backup of a pre-existing config (overwrote)
}

// confirmKubeconfigOverwrite decides whether to overwrite a pre-existing
// ~/.kube/config. Indirected through a var so tests can stub the prompt.
var confirmKubeconfigOverwrite = promptYesNo

func globalKubePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

func kubeStatePath(repo string) string {
	return filepath.Join(repo, "artifacts", "kube-global.json")
}

// installGlobalKubeconfig copies the PoC kubeconfig to ~/.kube/config so
// kubectl / k9s / etc. work without setting KUBECONFIG. Behaviour:
//   - we already manage it (state file present) → just refresh it, no prompt
//   - ~/.kube/config absent → create it
//   - ~/.kube/config present → ask to overwrite; on yes, back the original
//     up first so destroy can restore it; on no (or non-interactive), skip
//
// It records what it did in artifacts/kube-global.json for destroy.
func installGlobalKubeconfig(out io.Writer, repo, srcPath string) error {
	dst, err := globalKubePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcPath, err)
	}

	// Idempotent: if we already manage ~/.kube/config (prior cluster up
	// in this PoC), just refresh the contents — don't re-prompt and don't
	// back up our own file as if it were the user's.
	if st, err := readKubeState(repo); err == nil && (st.Action == "created" || st.Action == "overwrote") {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return err
		}
		fmt.Fprintf(out, "      refreshed %s\n", dst)
		return nil
	}

	state := globalKubeState{Path: dst}
	if _, statErr := os.Stat(dst); statErr == nil {
		// Pre-existing config — ask before clobbering it.
		if !confirmKubeconfigOverwrite(out, "~/.kube/config exists. Overwrite it to point at this cluster (a backup is kept)? [y/N]: ") {
			fmt.Fprintf(out, "      left ~/.kube/config untouched — use: export KUBECONFIG=%s\n", srcPath)
			state.Action = "skipped"
			return writeKubeState(repo, state)
		}
		bak := dst + ".ocibnkctl-bak"
		if err := os.Rename(dst, bak); err != nil {
			return fmt.Errorf("back up %s: %w", dst, err)
		}
		state.Action = "overwrote"
		state.Backup = bak
		fmt.Fprintf(out, "      backed up existing config → %s\n", bak)
	} else {
		state.Action = "created"
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	fmt.Fprintf(out, "      wrote %s — kubectl / k9s now talk to this cluster directly\n", dst)
	return writeKubeState(repo, state)
}

// removeGlobalKubeconfig undoes installGlobalKubeconfig per the recorded
// state: remove the file we created, or restore the backup we made. A
// no-op when we never touched ~/.kube/config (no state, or skipped).
func removeGlobalKubeconfig(out io.Writer, repo string) {
	st, err := readKubeState(repo)
	if err != nil {
		return
	}
	switch st.Action {
	case "created":
		if err := os.Remove(st.Path); err == nil {
			fmt.Fprintf(out, "      removed %s\n", st.Path)
		}
	case "overwrote":
		if st.Backup != "" {
			if err := os.Rename(st.Backup, st.Path); err == nil {
				fmt.Fprintf(out, "      restored your previous %s from backup\n", st.Path)
			} else if rmErr := os.Remove(st.Path); rmErr == nil {
				fmt.Fprintf(out, "      removed %s (backup left at %s)\n", st.Path, st.Backup)
			}
		} else if err := os.Remove(st.Path); err == nil {
			fmt.Fprintf(out, "      removed %s\n", st.Path)
		}
	}
	_ = os.Remove(kubeStatePath(repo))
}

func writeKubeState(repo string, s globalKubeState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(kubeStatePath(repo), b, 0o600)
}

func readKubeState(repo string) (globalKubeState, error) {
	var s globalKubeState
	b, err := os.ReadFile(kubeStatePath(repo))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// promptYesNo reads a y/N answer from stdin. Returns false when stdin is
// not a terminal (so automation / CI never hangs or clobbers a config).
func promptYesNo(out io.Writer, question string) bool {
	if !stdinIsTerminal() {
		fmt.Fprintln(out, "      (non-interactive — leaving ~/.kube/config as is)")
		return false
	}
	fmt.Fprint(out, "      "+question)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(sc.Text()))
	return ans == "y" || ans == "yes"
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
