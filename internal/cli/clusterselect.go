package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/spf13/cobra"
)

// newClusterListCmd lists every k3s cluster running on the host (across all
// PoCs), marking the one ~/.kube/config currently points at.
func newClusterListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all k3s clusters running on this host",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rt, err := cluster.Detect(ctx, "")
			if err != nil {
				return err
			}
			infos, err := cluster.ListClusters(ctx, rt)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no ocibnkctl clusters found on this host")
				return nil
			}
			curPort := currentKubePort()
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  \tNAME\tNODES\tAPI PORT\tKUBECONFIG")
			for _, ci := range infos {
				marker := " "
				active := ""
				if ci.APIPort != "" && ci.APIPort == curPort {
					marker, active = "*", "← ~/.kube/config"
				}
				fmt.Fprintf(w, "%s\t%s\t%d/%d\t%s\t%s\n",
					marker, ci.Name, ci.Running, ci.Nodes, dashIfEmpty(ci.APIPort), active)
			}
			w.Flush()
			fmt.Fprintf(cmd.OutOrStdout(), "\nselect one: %s cluster use <name>\n", invocationName())
			return nil
		},
	}
}

// newClusterUseCmd points ~/.kube/config at a chosen cluster (re-deriving the
// host-mapped API port, which changes on every docker restart). With no name it
// prints a numbered menu and reads a selection from stdin; when exactly one
// cluster exists it is the default — just hit Enter to pick it.
func newClusterUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use [name]",
		Short: "Point ~/.kube/config at a cluster (interactive if no name given)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			rt, err := cluster.Detect(ctx, "")
			if err != nil {
				return err
			}
			infos, err := cluster.ListClusters(ctx, rt)
			if err != nil {
				return err
			}
			if len(infos) == 0 {
				return fmt.Errorf("no ocibnkctl clusters found on this host")
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
			} else {
				if name, err = pickCluster(cmd, infos); err != nil {
					return err
				}
			}

			var chosen *cluster.ClusterInfo
			for i := range infos {
				if infos[i].Name == name {
					chosen = &infos[i]
					break
				}
			}
			if chosen == nil {
				return fmt.Errorf("no cluster named %q (see `%s cluster list`)", name, invocationName())
			}

			kc, err := cluster.ReadKubeconfig(ctx, rt, chosen.Name)
			if err != nil {
				return err
			}
			dst, err := globalKubePath()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			// Back up the user's pre-existing config once (same name the cluster-up
			// path uses), so we never silently clobber a hand-managed config.
			if _, statErr := os.Stat(dst); statErr == nil {
				bak := dst + ".ocibnkctl-bak"
				if _, bErr := os.Stat(bak); os.IsNotExist(bErr) {
					if data, rErr := os.ReadFile(dst); rErr == nil {
						_ = os.WriteFile(bak, data, 0o600)
						fmt.Fprintf(cmd.OutOrStdout(), "backed up existing config → %s\n", bak)
					}
				}
			}
			if err := os.WriteFile(dst, kc, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", dst, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "~/.kube/config → cluster %q (API 127.0.0.1:%s)\n", chosen.Name, chosen.APIPort)
			return nil
		},
	}
}

// pickCluster prints a numbered menu and reads a 1-based selection from stdin.
// When exactly one cluster is present it is the default: hitting Enter (empty
// input) selects it, so the common single-cluster case needs no typing.
func pickCluster(cmd *cobra.Command, infos []cluster.ClusterInfo) (string, error) {
	out := cmd.OutOrStdout()
	curPort := currentKubePort()
	// Default selection: with a single cluster, Enter selects it.
	defaultIdx := 0 // 1-based; 0 = no default
	if len(infos) == 1 {
		defaultIdx = 1
	}
	fmt.Fprintln(out, "running clusters:")
	for i, ci := range infos {
		active := ""
		if ci.APIPort != "" && ci.APIPort == curPort {
			active = "  (current)"
		}
		def := ""
		if i+1 == defaultIdx {
			def = "  [default]"
		}
		fmt.Fprintf(out, "  %d) %s  [%d/%d nodes]%s%s\n", i+1, ci.Name, ci.Running, ci.Nodes, active, def)
	}
	prompt := "select [1-" + strconv.Itoa(len(infos)) + "]"
	if defaultIdx > 0 {
		prompt += " (default " + strconv.Itoa(defaultIdx) + ", just hit Enter)"
	}
	fmt.Fprint(out, prompt+": ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	sel := strings.TrimSpace(line)
	if sel == "" {
		if defaultIdx > 0 {
			return infos[defaultIdx-1].Name, nil
		}
		return "", fmt.Errorf("no selection")
	}
	n, err := strconv.Atoi(sel)
	if err != nil || n < 1 || n > len(infos) {
		return "", fmt.Errorf("invalid selection %q", sel)
	}
	return infos[n-1].Name, nil
}

// currentKubePort returns the 127.0.0.1:<port> the active ~/.kube/config points
// at (empty if none / unreadable), used to mark the active cluster.
func currentKubePort() string {
	dst, err := globalKubePath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		return ""
	}
	return parseKubePort(data)
}

// parseKubePort extracts the 127.0.0.1:<port> a kubeconfig points at (empty if
// none), used to mark/identify the active cluster.
func parseKubePort(data []byte) string {
	const marker = "https://127.0.0.1:"
	idx := strings.Index(string(data), marker)
	if idx < 0 {
		return ""
	}
	rest := string(data)[idx+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
