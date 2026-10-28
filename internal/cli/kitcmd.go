package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/version"
	"github.com/mwiget/ocibnkctl/kit"
)

// kitMarker identifies a directory `kit extract` wrote, so --force only
// ever replaces a previous extract.
const kitMarker = ".bnk-kit"

func newKitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kit",
		Short: "The plain-file BNK kit (install scripts, scenarios) this binary carries",
	}
	cmd.AddCommand(newKitExtractCmd())
	return cmd
}

func newKitExtractCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "extract <dir>",
		Short: "Write the kit (platform install scripts, scenarios, helpers) to a directory",
		Long: `Write the kit this binary embeds to <dir>: kit/platform (the BNK install as
shell scripts), kit/scenarios (the test scenarios), kit/lib (their helpers)
and kit/README.md. The scripts need only sh, kubectl and helm, so they run
on any cluster without ocibnkctl — see <dir>/README.md.

The same tree is published as bnk-kit_<version>.tar.gz with each release.

  ocibnkctl kit extract ./bnk-kit`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKitExtract(cmd.OutOrStdout(), args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Replace a previous extract in <dir>")
	return cmd
}

func runKitExtract(out io.Writer, dir string, force bool) error {
	dest, err := filepath.Abs(expandTilde(dir))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dest)
	switch {
	case os.IsNotExist(err) || (err == nil && len(entries) == 0):
	case err != nil:
		return err
	default:
		if _, err := os.Stat(filepath.Join(dest, kitMarker)); err != nil {
			return fmt.Errorf("%s is not empty and is not a previous kit extract — choose another directory", dest)
		}
		if !force {
			return fmt.Errorf("%s holds a previous kit extract — pass --force to replace it", dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	if err := kit.ExtractAll(dest); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, kitMarker), []byte(version.Version+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "Kit (ocibnkctl %s, BNK %s) → %s\n", version.Version, version.BNKVersion, dest)
	fmt.Fprintf(out, "Start with %s\n", filepath.Join(dest, "README.md"))
	return nil
}
