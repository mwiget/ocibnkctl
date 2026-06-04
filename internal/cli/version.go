package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/version"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print ocibnkctl build info + pinned BNK / k8s versions",
		Run: func(cmd *cobra.Command, args []string) {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "ocibnkctl    %s (commit %s, built %s)\n",
				version.Version, version.Commit, version.BuildDate)
			fmt.Fprintf(out, "BNK target    %s\n", version.BNKVersion)
			fmt.Fprintf(out, "k8s           %s   (%s)\n", version.K8sVersion, version.K3sNodeImage)
			fmt.Fprintf(out, "CNE manifest  %s\n", version.CNEManifestVersion)
			fmt.Fprintf(out, "cert-manager  %s\n", version.CertManagerVersion)
			fmt.Fprintf(out, "calico        %s\n", version.CalicoManifestURL)
			fmt.Fprintln(out)
			if version.Measured() {
				fmt.Fprintf(out, "min baseline           %d cores / %d GB\n",
					version.MinBaseline.Cores, version.MinBaseline.MemoryGB)
				fmt.Fprintf(out, "min w/ bnk-forge       %d cores / %d GB\n",
					version.MinWithBNKForge.Cores, version.MinWithBNKForge.MemoryGB)
			} else {
				fmt.Fprintln(out, "min resources         not yet measured (TBD)")
			}
		},
	}
}
