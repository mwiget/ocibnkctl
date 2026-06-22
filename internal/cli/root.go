package cli

import (
	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/version"
)

// NewRootCmd assembles the cobra command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ocibnkctl",
		Short: "Deploy F5 BIG-IP Next for Kubernetes (BNK) on a native k3s cluster with demo-mode TMM",
		Long: `ocibnkctl provisions a BNK ` + version.BNKVersion + ` deployment on a
native k3s cluster: one dedicated control node (tainted
control-plane:NoSchedule) plus N worker nodes (cluster.tmm_nodes), each
labelled app=f5-tmm. FLO runs TMM in demo mode as a wholeCluster
DaemonSet — one TMM per labelled worker — so the data plane scales out
with the worker count. The k3s nodes run directly as containers on the
host OCI runtime (docker or podman) — no third-party orchestrator binary:

  cluster up  -> create the k3s cluster, install Calico, dedicate the
                 control node, label the TMM workers, fetch kubeconfig
  deploy      -> install BNK platform (cert-manager, FLO, License CR,
                 CNEInstance with wholeCluster + demoMode=true)

Each PoC lives in its own local dir (see "init"). poc.yaml holds the
full declarative state needed to tear down and redeploy.

Agentic: each PoC ships an AGENTS.md operator+agent guide. Point your
favorite agentic CLI at it to deploy and manage BNK conversationally —
see "ocibnkctl agent --help".

Run "ocibnkctl doctor" after install to verify docker/podman, kubectl,
and helm are reachable.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(
		newInitCmd(),
		newValidateCmd(),
		newDoctorCmd(),
		newClusterCmd(),
		newScaleCmd(),
		newDeployCmd(),
		newDestroyCmd(),
		newE2ECmd(),
		newScenarioCmd(),
		newBNKForgeCmd(),
		newAgentCmd(),
		newVersionCmd(),
	)
	return root
}
