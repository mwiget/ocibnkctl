package cli

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/embedded"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
)

func newInitCmd() *cobra.Command {
	var (
		dir      string
		customer string
		noGit    bool
	)
	cmd := &cobra.Command{
		Use:   "init <poc-name>",
		Short: "Create a new PoC repo (poc.yaml + agent files + keys/ skeleton)",
		Long: `Create a new PoC repo at ./<poc-name> (or --dir <path>).

The repo contains:
  poc.yaml         declarative state — source of truth for tear-down + redeploy
  AGENTS.md        instructions for any agentic CLI driving this PoC
  CLAUDE.md        @AGENTS.md include for Claude Code
  journal/         append-only markdown log written during the PoC
  artifacts/       rendered k3s.yaml, kubeconfig, helm values
  keys/            gitignored — drop FAR tgz + JWT here
  .gitignore       excludes all secret material

Initializes a git repo unless --no-git.

Auto-detects bnk-forge: if $OCIBNKCTL_BNK_FORGE_PATH or ~/git/bnk-forge
exists (with a Makefile inside), the bnk_forge: block is pre-filled and
enabled. Otherwise it's written disabled. Either way, deployment never
blocks on bnk-forge presence.

Env overrides (for argv+env runners — BNK Forge container modules, CI —
that cannot hand-edit poc.yaml). Unset means "keep the default"; an
invalid value is an error, never a silent fallback:

  OCIBNKCTL_CUSTOMER      metadata.customer      (--customer wins)
  OCIBNKCTL_PROVIDER      cluster.provider       docker | podman
  OCIBNKCTL_TMM_NODES     cluster.tmm_nodes      positive integer
  OCIBNKCTL_EDGE_OCTET    cluster.edge_octet     1-254 — give parallel
                                                 clusters distinct octets
  OCIBNKCTL_HOST_PROFILE  bnk.host_profile       standard | small
                                                 (overrides host auto-detect)
  OCIBNKCTL_TEEMS_RELAY   cluster.teems_relay    true | false`,
		Args: initArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !validName(name) {
				return fmt.Errorf("invalid PoC name %q: use [a-z0-9-]+", name)
			}
			target := dir
			if target == "" {
				target = "./" + name
			}
			target = expandTilde(target)
			abs, err := filepath.Abs(target)
			if err != nil {
				return err
			}
			// An existing directory is only fatal once it holds a PoC. Keying the
			// refusal on poc.yaml (not on the directory) keeps the "never clobber
			// an initialized PoC" guarantee while letting init adopt a directory
			// something else pre-seeded — BNK Forge materializes declared
			// secret_files into <poc>/keys/ before it runs this step, so the
			// target always exists there and a blanket refusal made the module
			// un-deployable.
			if info, err := os.Stat(abs); err == nil {
				if !info.IsDir() {
					return fmt.Errorf("refusing to overwrite: %s exists and is not a directory", abs)
				}
				if _, err := os.Stat(filepath.Join(abs, poc.FileName)); err == nil {
					return fmt.Errorf("refusing to overwrite: %s already holds a %s", abs, poc.FileName)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "adopting existing directory %s (no %s yet)\n", abs, poc.FileName)
			}
			if err := os.MkdirAll(abs, 0o755); err != nil {
				return err
			}

			// Skeleton dirs.
			for _, d := range []string{"journal", "artifacts", "keys"} {
				if err := os.MkdirAll(filepath.Join(abs, d), 0o755); err != nil {
					return err
				}
			}
			// .gitkeep so empty dirs survive git add.
			for _, d := range []string{"journal", "artifacts", "keys"} {
				if err := os.WriteFile(filepath.Join(abs, d, ".gitkeep"), nil, 0o644); err != nil {
					return err
				}
			}

			// Copy embedded files (AGENTS.md, CLAUDE.md, .gitignore).
			if err := copyEmbedded(abs); err != nil {
				return fmt.Errorf("copy templates: %w", err)
			}

			// Build poc.yaml, including bnk-forge auto-detect.
			p := poc.New(name)
			p.Metadata.Customer = customer
			if forgePath := detectBNKForge(); forgePath != "" {
				p.BNKForge.Enabled = true
				p.BNKForge.RepoPath = forgePath
				fmt.Fprintf(cmd.OutOrStdout(), "detected bnk-forge at %s — bnk_forge.enabled set true\n", forgePath)
			}
			// On a tight host, pin host_profile=small into poc.yaml so TMM sheds
			// its metrics sidecar (CNE telemetry.metricSubsystem=false) and fits a
			// sub-10-core node. Persisted here — not decided at deploy time — so
			// poc.yaml stays the single source of truth; edit it to "standard" to
			// force the full footprint. Mirrors the auto deploy-shrink rule.
			if _, autoSmall := p.BNK.ResolveHostProfile(runtime.NumCPU(), version.MinBaseline.Cores); autoSmall {
				p.BNK.HostProfile = poc.HostProfileSmall
				fmt.Fprintf(cmd.OutOrStdout(),
					"host has %d cores < %d-core floor — set bnk.host_profile=small in poc.yaml (TMM metrics subsystem off)\n",
					runtime.NumCPU(), version.MinBaseline.Cores)
			}
			// Env overrides last: an explicit OCIBNKCTL_* value must win over
			// the host auto-detect above (e.g. host_profile=standard on a tight
			// host). See initenv.go.
			if err := applyEnvOverrides(p, cmd.OutOrStdout(), cmd.Flags().Changed("customer")); err != nil {
				return err
			}
			if err := p.Save(abs); err != nil {
				return err
			}

			// Initial journal entry.
			j := fmt.Sprintf("# %s — PoC initialized\n\nCreated with ocibnkctl. Next: drop keys/ files (FAR tgz + JWT), then `ocibnkctl validate` and `ocibnkctl e2e --yolo --confirm-cluster %s`.\n",
				time.Now().UTC().Format("2006-01-02"), name)
			if err := os.WriteFile(
				filepath.Join(abs, "journal", time.Now().UTC().Format("2006-01-02")+"-init.md"),
				[]byte(j), 0o644); err != nil {
				return err
			}

			if !noGit {
				if err := gitInit(abs); err != nil {
					// non-fatal — operator may not have git
					fmt.Fprintf(cmd.OutOrStdout(), "WARN: git init failed (%v) — continuing\n", err)
				}
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "PoC repo created at %s\n\n", abs)
			fmt.Fprintln(out, "Next:")
			fmt.Fprintf(out, "  cd %s\n", target)
			fmt.Fprintln(out, "  # drop FAR tgz + JWT into keys/")
			fmt.Fprintln(out, "  cp /path/to/f5-far-auth-key.tgz keys/")
			fmt.Fprintln(out, "  cp /path/to/license.jwt          keys/.jwt")
			fmt.Fprintln(out, "  ocibnkctl validate")
			fmt.Fprintf(out, "  ocibnkctl e2e --yolo --confirm-cluster %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "target directory (default ./<poc-name>)")
	cmd.Flags().StringVar(&customer, "customer", "", "customer name recorded in poc.yaml.metadata.customer")
	cmd.Flags().BoolVar(&noGit, "no-git", false, "skip git init")
	return cmd
}

func initArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("PoC name required\n\nUsage: ocibnkctl init <poc-name>")
	}
	return nil
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`)

func validName(s string) bool { return nameRE.MatchString(s) }

// detectBNKForge looks for a local bnk-forge clone. Checks
// $OCIBNKCTL_BNK_FORGE_PATH first, then ~/git/bnk-forge. Returns the
// path if a directory containing a Makefile is found, else "".
func detectBNKForge() string {
	candidates := []string{}
	if env := strings.TrimSpace(os.Getenv("OCIBNKCTL_BNK_FORGE_PATH")); env != "" {
		candidates = append(candidates, expandTilde(env))
	}
	if h, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(h, "git", "bnk-forge"))
	}
	for _, c := range candidates {
		mk := filepath.Join(c, "Makefile")
		if _, err := os.Stat(mk); err == nil {
			return c
		}
	}
	return ""
}

// copyEmbedded walks embedded.FS (files/) and copies each entry into
// the PoC repo root. .gitignore is renamed from poc.gitignore so the
// template tree can be committed.
func copyEmbedded(dest string) error {
	return fs.WalkDir(embedded.FS, "files", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, "files/")
		out := filepath.Join(dest, rel)
		// poc.gitignore → .gitignore
		if rel == "poc.gitignore" {
			out = filepath.Join(dest, ".gitignore")
		}
		body, err := embedded.FS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	})
}

func gitInit(dir string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}
