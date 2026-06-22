package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
)

type scaleFlags struct {
	pocDir         string
	tmm            int
	yolo           bool
	confirmCluster string
}

func newScaleCmd() *cobra.Command {
	f := &scaleFlags{}
	cmd := &cobra.Command{
		Use:   "scale",
		Short: "Scale the number of TMM nodes up or down (DESTRUCTIVE)",
		Long: `Change how many worker nodes host TMM (cluster.tmm_nodes), live.

Under wholeCluster, FLO runs TMM as a DaemonSet, so the TMM count simply
tracks the number of app=f5-tmm-labelled worker nodes — there is no
tmmReplicas to patch.

Scale up:   join new k3s agent node(s) and label them app=f5-tmm; the
            f5-tmm DaemonSet auto-schedules a TMM pod onto each.
Scale down: unlabel the surplus node(s) so the DaemonSet drains TMM off
            them, then remove the surplus agent node container(s).

The new tmm_nodes value is written back to poc.yaml so the next
deploy/e2e renders the same count.

Each TMM is active and serves the traffic that lands on its own node.
NOTE: transparently fanning one VIP's throughput across TMM nodes needs
DPU/SR-IOV and is not available in the demo shape — adding nodes scales
availability / per-node capacity, not a single VIP's throughput.

Required gates:
  --yolo                   acknowledge cluster + in-cluster writes
  --confirm-cluster NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScale(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().IntVar(&f.tmm, "tmm", 0, "Desired number of TMM nodes (1..MaxTMMNodes)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge scaling is destructive")
	cmd.Flags().StringVar(&f.confirmCluster, "confirm-cluster", "", "Must equal poc.yaml.metadata.name (typo guard)")
	return cmd
}

func runScale(ctx context.Context, out io.Writer, f *scaleFlags) error {
	repo, err := resolvePoCDir(f.pocDir)
	if err != nil {
		return err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
	}
	if err := requireTwoGates(f.yolo, "--confirm-cluster", f.confirmCluster,
		p.Metadata.Name, "TMM scaling"); err != nil {
		return err
	}
	target := f.tmm
	if target < 1 || target > poc.MaxTMMNodes {
		return fmt.Errorf("--tmm %d: must be 1..%d", target, poc.MaxTMMNodes)
	}
	current := p.Cluster.Workers()
	fmt.Fprintf(out, "PoC: %s — scaling TMM nodes %d → %d\n\n", p.Metadata.Name, current, target)
	if target == current {
		fmt.Fprintln(out, "Already at the requested TMM node count — nothing to do.")
		return nil
	}

	rt, err := cluster.Detect(ctx, cluster.Runtime(p.Cluster.Provider))
	if err != nil {
		return err
	}
	prov, err := newProvisioner(rt, prefixWriter{w: out, prefix: "      | "})
	if err != nil {
		return err
	}
	if err := prov.EnsurePresent(); err != nil {
		return err
	}
	// Mirror the registry-cache wiring so scaled-up worker nodes pull through
	// the same fleet as the originals (no-op when disabled). Before AddWorker.
	if msg, err := applyRegistryCache(prov, p, repo); err != nil {
		return err
	} else if msg != "" {
		fmt.Fprintf(out, "      %s\n", msg)
	}
	nodeImage := p.Versions.NodeImage
	if nodeImage == "" {
		nodeImage = prov.DefaultNodeImage()
	}
	r := &deploy.Runner{
		KubeconfigPath: filepath.Join(repo, "artifacts", "kubeconfig"),
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
	}
	labelKey, labelVal := p.BNK.TMMLabel()

	if target > current {
		// Scale up: add + label the new node(s). Labelling app=f5-tmm is all
		// it takes — the f5-tmm DaemonSet auto-schedules a TMM pod onto each
		// newly-labelled node. No replica field to bump.
		names := prov.WorkerNodeNames(p.Cluster.Name, target)
		for i := current; i < target; i++ {
			node := names[i]
			fmt.Fprintf(out, "[+] joining TMM node %s ...\n", node)
			if err := prov.AddWorker(ctx, p.Cluster.Name, i, nodeImage); err != nil {
				return err
			}
			if err := r.Kubectl(ctx, "label", "node", node,
				fmt.Sprintf("%s=%s", labelKey, labelVal), "--overwrite"); err != nil {
				return fmt.Errorf("label %s: %w", node, err)
			}
		}
		fmt.Fprintf(out, "[*] %d node(s) labelled %s=%s — the f5-tmm DaemonSet schedules a TMM on each.\n",
			target-current, labelKey, labelVal)
	} else {
		// Scale down: unlabel the surplus nodes FIRST so the DaemonSet drains
		// its TMM pod off them, give it a moment, then remove the node
		// containers from the highest index down.
		names := prov.WorkerNodeNames(p.Cluster.Name, current)
		for i := current - 1; i >= target; i-- {
			node := names[i]
			fmt.Fprintf(out, "[*] unlabelling %s (%s-) so the DaemonSet drains TMM off it ...\n", node, labelKey)
			if err := r.Kubectl(ctx, "label", "node", node, labelKey+"-"); err != nil {
				fmt.Fprintf(out, "      WARN: could not unlabel %s: %v\n", node, err)
			}
		}
		fmt.Fprintln(out, "      waiting 20s for TMM to drain off surplus node(s) ...")
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Second):
		}
		for i := current - 1; i >= target; i-- {
			node := names[i]
			fmt.Fprintf(out, "[-] removing TMM node %s ...\n", node)
			// Drop the k8s node object too so it doesn't linger NotReady.
			_ = r.Kubectl(ctx, "delete", "node", node, "--ignore-not-found")
			if err := prov.RemoveWorker(ctx, p.Cluster.Name, i); err != nil {
				return err
			}
		}
	}

	// Persist the new count so deploy/e2e renders it next time.
	p.Cluster.TMMNodes = target
	if err := p.Save(repo); err != nil {
		return fmt.Errorf("save poc.yaml: %w", err)
	}
	fmt.Fprintf(out, "\nDone — cluster.tmm_nodes=%d persisted to poc.yaml.\n", target)
	fmt.Fprintln(out, "Each TMM serves the traffic that lands on its own node (per-node active/active).")
	return nil
}
