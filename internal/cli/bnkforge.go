package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

func newBNKForgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bnk-forge",
		Short: "Manually drive the bnk-forge integration (launch / unregister)",
	}
	cmd.AddCommand(newBNKForgeLaunchCmd())
	cmd.AddCommand(newBNKForgeUnregisterCmd())
	return cmd
}

func newBNKForgeLaunchCmd() *cobra.Command {
	var pocDir string
	cmd := &cobra.Command{
		Use:   "launch",
		Short: "Ensure bnk-forge sees this PoC's cluster (create project + register kubeconfig)",
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolvePoCDir(pocDir)
			if err != nil {
				return err
			}
			p, err := poc.Load(repo)
			if err != nil {
				return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
			}
			if !p.BNKForge.Enabled {
				return errors.New("bnk_forge.enabled is false in poc.yaml — flip it true and retry")
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "PoC: %s\n\n", p.Metadata.Name)
			ctx := cmd.Context()
			rt, err := cluster.Detect(ctx, cluster.Runtime(p.Cluster.Provider))
			if err != nil {
				return err
			}
			prov, err := newProvisioner(rt, prefixWriter{w: out, prefix: "      | "})
			if err != nil {
				return err
			}
			dc := &cluster.DockerCLI{Runtime: rt, Out: prefixWriter{w: out, prefix: "      | "}}
			if err := registerWithBNKForge(ctx, out, repo, p, dc, prov.ServerNodeName(p.Cluster.Name)); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pocDir, "poc", "", "PoC repo path (default: current directory)")
	return cmd
}

func newBNKForgeUnregisterCmd() *cobra.Command {
	var pocDir string
	cmd := &cobra.Command{
		Use:   "unregister",
		Short: "Remove this PoC's cluster + project from bnk-forge",
		RunE: func(cmd *cobra.Command, args []string) error {
			repo, err := resolvePoCDir(pocDir)
			if err != nil {
				return err
			}
			p, err := poc.Load(repo)
			if err != nil {
				return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
			}
			if !p.BNKForge.Enabled {
				return errors.New("bnk_forge.enabled is false in poc.yaml — nothing to unregister")
			}
			return unregisterFromBNKForge(cmd.Context(), cmd.OutOrStdout(), p)
		},
	}
	cmd.Flags().StringVar(&pocDir, "poc", "", "PoC repo path (default: current directory)")
	return cmd
}
