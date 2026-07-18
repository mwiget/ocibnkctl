package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/bnkforge"
	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/embedded"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
)

func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Bring up (or down) the k3s cluster",
	}
	cmd.AddCommand(newClusterUpCmd(), newClusterListCmd(), newClusterUseCmd())
	return cmd
}

type clusterUpFlags struct {
	pocDir         string
	yolo           bool
	confirmCluster string
	skipBNKForge   bool
	skipKubeconfig bool
}

func newClusterUpCmd() *cobra.Command {
	f := &clusterUpFlags{}
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Create the k3s cluster, install Calico, label TMM worker (DESTRUCTIVE)",
		Long: `Drive the k3s cluster bring-up:

  1. Container-runtime preflight
  2. Render k3s.yaml + ensure cluster exists (start k3s server + agent
     containers and join them; bundled flannel is disabled)
  3. Fetch kubeconfig to artifacts/kubeconfig (mode 0600)
  4. Apply Calico CNI + NetworkAttachmentDefinition CRD
  5. Label the worker node app=f5-tmm for TMM nodeSelector
  6. If bnk_forge.enabled and the local stack is reachable, register
     the cluster with bnk-forge. Soft-skip on absence.

Required gates:
  --yolo                   acknowledge the cluster is recreated/written
  --confirm-cluster NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterUp(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge cluster creation is destructive")
	cmd.Flags().StringVar(&f.confirmCluster, "confirm-cluster", "", "Must equal poc.yaml.metadata.name (typo guard)")
	cmd.Flags().BoolVar(&f.skipBNKForge, "skip-bnk-forge", false, "Skip bnk-forge auto-registration even if enabled")
	cmd.Flags().BoolVar(&f.skipKubeconfig, "skip-kubeconfig", false, "Don't install the cluster kubeconfig as ~/.kube/config")
	return cmd
}

// calicoEdgeExclude renders the Calico CRs that keep the bnk-edge subnet (and
// the off-subnet egress dests) out of Calico's pod-IPAM and out of its
// node-to-node BGP mesh, so attaching workers to the shared bnk-edge L2 doesn't
// collide with Calico routes. Applied before EnsureEdge attaches the workers.
func calicoEdgeExclude(octet int) string {
	return fmt.Sprintf(`apiVersion: crd.projectcalico.org/v1
kind: IPPool
metadata:
  name: bnk-edge-exclude
spec:
  cidr: 192.168.%d.0/24
  disabled: true
---
apiVersion: crd.projectcalico.org/v1
kind: IPPool
metadata:
  name: bnk-egress-dest-exclude
spec:
  cidr: 198.51.0.0/16
  disabled: true
---
apiVersion: crd.projectcalico.org/v1
kind: BGPConfiguration
metadata:
  name: default
spec:
  nodeToNodeMeshEnabled: true
  ignoredInterfaces:
  - br-bnk-bgp
`, octet)
}

func runClusterUp(ctx context.Context, out io.Writer, f *clusterUpFlags) error {
	repo, err := resolvePoCDir(f.pocDir)
	if err != nil {
		return err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
	}
	if err := requireTwoGates(f.yolo, "--confirm-cluster", f.confirmCluster,
		p.Metadata.Name, "cluster bring-up"); err != nil {
		return err
	}
	if r := p.Validate(); !r.Valid() {
		for _, e := range r.Errors {
			fmt.Fprintln(out, "  ✗", e)
		}
		return fmt.Errorf("poc.yaml is invalid — fix above and re-run `ocibnkctl validate`")
	}

	fmt.Fprintf(out, "PoC:        %s  (BNK %s)\n\n", p.Metadata.Name, p.Metadata.BNKVersion)

	// 1. Container runtime.
	fmt.Fprintln(out, "[1/6] Container-runtime preflight ...")
	rt, err := cluster.Detect(ctx, cluster.Runtime(p.Cluster.Provider))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "      using %s\n", rt)

	prov, err := newProvisioner(rt, prefixWriter{w: out, prefix: "      | "})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "      backend %s\n", prov.Backend())
	if err := prov.EnsurePresent(); err != nil {
		return err
	}
	// Opt-in: steer node containerd at the local pull-through cache fleet
	// (regcachectl). Renders + mounts registries.yaml on every node. Must run
	// before CreateCluster.
	if msg, err := applyRegistryCache(prov, p, repo); err != nil {
		return err
	} else if msg != "" {
		fmt.Fprintf(out, "      %s\n", msg)
	}

	// 2. Render the backend config + create cluster (idempotent).
	cfgName := prov.ConfigArtifact()
	fmt.Fprintf(out, "[2/6] Rendering %s + ensuring cluster exists ...\n", cfgName)
	clusterCfg, err := prov.RenderConfig(p.Cluster.Name, p.Cluster.Workers())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(repo, "artifacts"), 0o755); err != nil {
		return err
	}
	rendered := filepath.Join(repo, "artifacts", cfgName)
	if err := os.WriteFile(rendered, []byte(clusterCfg), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "      rendered → %s\n", rendered)
	exists, err := prov.ClusterExists(ctx, p.Cluster.Name)
	if err != nil {
		return err
	}
	if exists {
		fmt.Fprintf(out, "      cluster %q already exists — leaving in place\n", p.Cluster.Name)
	} else {
		// node_image from poc.yaml (defaults to the pinned rancher/k3s
		// image); fall back to the backend default if unset.
		nodeImage := p.Versions.NodeImage
		if nodeImage == "" {
			nodeImage = prov.DefaultNodeImage()
		}
		if err := prov.CreateCluster(ctx, p.Cluster.Name, clusterCfg, nodeImage, p.Cluster.Workers()); err != nil {
			return err
		}
	}

	// 3. Fetch kubeconfig early — Calico apply uses it.
	fmt.Fprintln(out, "[3/6] Fetching kubeconfig ...")
	kubeconfigPath := filepath.Join(repo, "artifacts", "kubeconfig")
	if err := prov.WriteKubeconfig(ctx, p.Cluster.Name, kubeconfigPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "      %s\n", kubeconfigPath)

	// 4. Apply Calico + NetworkAttachmentDefinition CRD. The NAD CRD
	// is a hard runtime dependency for FLO's manager startup even in
	// demo mode (where no NADs are actually used) — without it, FLO's
	// controller-runtime informers stall and the crd-installer never
	// reconciles the License CRD. We install just the CRD (not the
	// full Multus daemonset) since the cluster does not actually
	// route through Multus.
	fmt.Fprintln(out, "[4/6] Applying Calico CNI + NetworkAttachmentDefinition CRD ...")
	r := &deploy.Runner{
		KubeconfigPath: kubeconfigPath,
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
	}
	if err := r.Kubectl(ctx, "apply", "-f", version.CalicoManifestURL); err != nil {
		return err
	}
	nadCRD, err := embedded.Templates.ReadFile("templates/nad-crd.yaml")
	if err != nil {
		return fmt.Errorf("read embedded nad-crd.yaml: %w", err)
	}
	if err := r.Apply(ctx, string(nadCRD)); err != nil {
		return fmt.Errorf("apply NetworkAttachmentDefinition CRD: %w", err)
	}
	// BNK PVCs request storageClassName: standard (kind's default SC
	// name). k3s names its default local-path, so add a `standard` SC
	// backed by the same provisioner or every BNK PVC stays Pending.
	standardSC, err := embedded.Templates.ReadFile("templates/standard-sc.yaml")
	if err != nil {
		return fmt.Errorf("read embedded standard-sc.yaml: %w", err)
	}
	if err := r.Apply(ctx, string(standardSC)); err != nil {
		return fmt.Errorf("apply standard StorageClass: %w", err)
	}
	// Wait for Calico controller — gives the CNI rollout enough time
	// that subsequent `kubectl` calls don't race it.
	if err := r.Wait(ctx, "kube-system", "Available", "deployment/calico-kube-controllers",
		5*time.Minute); err != nil {
		fmt.Fprintf(out, "      WARN: calico-kube-controllers not Available in 5min: %v\n", err)
	}

	// Repoint CoreDNS at the host's real upstream resolvers. The k3s default
	// `forward . /etc/resolv.conf` chains through docker's embedded DNS
	// (127.0.0.11) on k3s-in-docker, which SERVFAILs external names under load
	// and makes F5 CNE license device registration fail on its one-shot attempt.
	// Best-effort — warns and continues if anything goes wrong.
	fmt.Fprintln(out, "      Configuring CoreDNS upstream resolvers ...")
	configureCoreDNS(ctx, out, r, rt, p.Cluster.Name)

	// 5. Label the worker node for TMM. We dropped the
	// bnk-internal / bnk-external docker bridges that earlier
	// versions of ocibnkctl attached to the node containers — no
	// scenario actually consumed them, and the Gateway IP pool
	// (203.0.113.0/24) is plumbed entirely via the bnk-bgp Multus
	// NAD bridge that scenarios create on demand.
	dc := &cluster.DockerCLI{Runtime: rt, Out: prefixWriter{w: out, prefix: "      | "}}
	nodes, err := dc.NodeContainers(ctx, prov.NodeContainerLabel(p.Cluster.Name))
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no %s node containers found for cluster %q — does `%s` list it?", prov.Backend(), p.Cluster.Name, prov.Tool())
	}
	workerNodes := prov.WorkerNodeNames(p.Cluster.Name, p.Cluster.Workers())
	fmt.Fprintf(out, "[5/6] Labelling %d worker node(s) for TMM + dedicating the control node ...\n", len(workerNodes))
	labelKey, labelVal := p.BNK.TMMLabel()
	for _, workerNode := range workerNodes {
		if err := r.Kubectl(ctx, "label", "node", workerNode,
			fmt.Sprintf("%s=%s", labelKey, labelVal), "--overwrite"); err != nil {
			return fmt.Errorf("label %s %s=%s: %w", workerNode, labelKey, labelVal, err)
		}
	}
	// Dedicate the control node: k3s leaves the server node schedulable
	// (unlike kubeadm/kind), so without this every BNK control-plane pod
	// — and, in wholeCluster mode, TMM itself — could land on it. Taint it
	// control-plane:NoSchedule so the server runs only the k8s control
	// plane (apiserver/etcd/scheduler + the k3s system pods that already
	// tolerate this taint) and all BNK workloads land on the labelled
	// worker(s). NoSchedule (not NoExecute) never evicts a running pod, so
	// this is safe to (re-)apply on an existing cluster; --overwrite keeps
	// it idempotent across re-runs. This matches the production topology
	// (dedicated control node + N TMM workers) the tmmlite fork adopted.
	serverNode := prov.ServerNodeName(p.Cluster.Name)
	if err := r.Kubectl(ctx, "taint", "node", serverNode,
		"node-role.kubernetes.io/control-plane=:NoSchedule", "--overwrite"); err != nil {
		return fmt.Errorf("taint control node %s: %w", serverNode, err)
	}
	fmt.Fprintf(out, "      control node %s tainted control-plane:NoSchedule\n", serverNode)

	// Edge fabric: the shared bnk-edge docker network the workers dual-home
	// onto (br-bnk-bgp enslaves each worker's uplink so TMM net1 lands on the
	// shared L2), plus the external FRR BGP peer + external origin containers.
	// The control node is deliberately left OFF bnk-edge. This gives one
	// permanent external FRR as the BGP peer + curl vantage for every
	// data-plane scenario (no per-scenario scn-frr pod).
	//
	// Calico (the CNI here) would otherwise grab br-bnk-bgp's /24 for its pod
	// pool AND advertise each worker's connected /24 over its node mesh — both
	// collide on the shared bnk-edge L2 (docker refuses the second worker with
	// "conflicts with existing route"). Exclude the edge subnet from Calico
	// IPAM + mesh BEFORE attaching the workers.
	octet := p.Cluster.EdgeNet()
	fmt.Fprintln(out, "      calico: excluding bnk-edge subnet from IPAM + BGP mesh ...")
	if err := r.Apply(ctx, calicoEdgeExclude(octet)); err != nil {
		return fmt.Errorf("calico edge exclude: %w", err)
	}
	fmt.Fprintf(out, "      edge: bnk-edge net + external FRR (%s, AS %d) + origin (%s) ...\n",
		cluster.EdgeFRRIP(octet), cluster.EdgeFRRAS, cluster.EdgeOriginIP(octet))
	if err := dc.EnsureEdge(ctx, p.Cluster.Name, octet, workerNodes); err != nil {
		return fmt.Errorf("edge fabric: %w", err)
	}

	// Optional TEEMS egress relay — for hosts where forwarded pod egress is
	// lossy (the CWC's license POST is the only casualty; it ignores HTTPS_PROXY,
	// so we transparently re-originate its TEEMS dial through the host stack).
	if p.Cluster.TEEMSRelay {
		fmt.Fprintln(out, "      teems-relay: forwarded-egress workaround for the CWC license POST ...")
		if err := dc.EnsureTEEMSRelay(ctx, p.Cluster.Name); err != nil {
			return fmt.Errorf("teems relay: %w", err)
		}
	}

	// 6. bnk-forge auto-registration (best-effort).
	fmt.Fprintln(out, "[6/6] bnk-forge registration ...")
	if f.skipBNKForge || !p.BNKForge.Enabled {
		fmt.Fprintln(out, "      skipped (disabled or --skip-bnk-forge)")
	} else {
		if err := registerWithBNKForge(ctx, out, repo, p, dc, prov.ServerNodeName(p.Cluster.Name)); err != nil {
			if errors.Is(err, bnkforge.ErrNotRunning) {
				fmt.Fprintf(out, "      bnk-forge configured but not running — skipping. (%v)\n", err)
			} else {
				fmt.Fprintf(out, "      WARN: bnk-forge registration failed: %v\n", err)
			}
		}
	}

	p.Status.Cluster = "ready"
	p.Status.LastPhaseAt = time.Now().UTC()
	if err := savePoC(repo, p, out); err != nil {
		return err
	}
	if j, err := appendJournal(repo, "cluster", "cluster up — READY"); err == nil {
		fmt.Fprintf(j, "- cluster: %s\n- nodes: %s\n", p.Cluster.Name, strings.Join(nodes, ", "))
		j.Close()
	}

	// Install the cluster kubeconfig as ~/.kube/config so kubectl / k9s
	// work without setting KUBECONFIG. Opt out with --skip-kubeconfig;
	// destroy reverts it.
	if !f.skipKubeconfig {
		fmt.Fprintln(out, "Installing ~/.kube/config ...")
		if err := installGlobalKubeconfig(out, repo, kubeconfigPath); err != nil {
			fmt.Fprintf(out, "      WARN: could not install ~/.kube/config: %v\n", err)
		}
	}

	fmt.Fprintf(out, "\nDONE.  Next: `%s deploy prereqs && deploy flo && deploy cne` (or run e2e).\n", invocationName())
	return nil
}

// configureCoreDNS repoints the cluster's CoreDNS forward at the host's real
// upstream resolvers (discovered from the k3s node's resolv.conf) instead of the
// default `forward . /etc/resolv.conf`. On k3s-in-docker that default chains
// through docker's embedded resolver (127.0.0.11), which SERVFAILs external
// names under load and makes F5 CNE license device registration fail on its
// one-shot attempt. Best-effort: every failure warns and returns, leaving
// CoreDNS untouched so cluster-up still completes.
func configureCoreDNS(ctx context.Context, out io.Writer, r *deploy.Runner, rt cluster.Runtime, clusterName string) {
	ups, err := cluster.CoreDNSUpstreams(ctx, rt, clusterName)
	if err != nil {
		fmt.Fprintf(out, "      WARN: could not read node resolvers for CoreDNS: %v\n", err)
		return
	}
	if len(ups) == 0 {
		fmt.Fprintln(out, "      CoreDNS: no host upstreams discovered — leaving default forward")
		return
	}

	corefile, err := r.KubectlCapture(ctx, "-n", "kube-system", "get", "configmap", "coredns",
		"-o", "jsonpath={.data.Corefile}")
	if err != nil {
		fmt.Fprintf(out, "      WARN: could not read CoreDNS config: %v\n", err)
		return
	}
	const defaultForward = "forward . /etc/resolv.conf"
	if !strings.Contains(corefile, defaultForward) {
		// Already customized (or an unrecognized k3s layout) — don't clobber it.
		fmt.Fprintln(out, "      CoreDNS: non-default forward already set — leaving as-is")
		return
	}
	patched := strings.Replace(corefile, defaultForward, "forward . "+strings.Join(ups, " "), 1)
	body, err := json.Marshal(map[string]any{"data": map[string]string{"Corefile": patched}})
	if err != nil {
		fmt.Fprintf(out, "      WARN: CoreDNS patch encode failed: %v\n", err)
		return
	}
	// Make the patch durable: stop the k3s addon controller from re-applying
	// the packaged Corefile (which would drop the forward on the next k3s
	// restart/upgrade) by skipping the bundled coredns manifest. Best-effort —
	// if it fails the patch below still applies, it just won't survive a
	// re-reconcile. Done before the patch so no reconcile window can revert it.
	if err := cluster.DisableCoreDNSAddonReconcile(ctx, rt, clusterName); err != nil {
		fmt.Fprintf(out, "      WARN: could not disable coredns addon reconcile (patch won't survive a k3s restart): %v\n", err)
	} else {
		fmt.Fprintln(out, "      k3s coredns addon reconcile disabled (.skip) — patch is durable")
	}
	if err := r.Kubectl(ctx, "-n", "kube-system", "patch", "configmap", "coredns",
		"--type", "merge", "-p", string(body)); err != nil {
		fmt.Fprintf(out, "      WARN: CoreDNS patch failed: %v\n", err)
		return
	}
	if err := r.Kubectl(ctx, "-n", "kube-system", "rollout", "restart", "deployment", "coredns"); err != nil {
		fmt.Fprintf(out, "      WARN: CoreDNS rollout restart failed: %v\n", err)
		return
	}
	if err := r.Kubectl(ctx, "-n", "kube-system", "rollout", "status", "deployment", "coredns",
		"--timeout=90s"); err != nil {
		fmt.Fprintf(out, "      WARN: CoreDNS rollout did not complete: %v\n", err)
		return
	}
	fmt.Fprintf(out, "      CoreDNS → host upstreams %s\n", strings.Join(ups, ", "))
}

// registerWithBNKForge runs the same flow dpubnkctl's bnk-forge launcher
// uses: ensure running → login → ensure project → register cluster
// with the localized kubeconfig.
func registerWithBNKForge(ctx context.Context, out io.Writer, repo string, p *poc.PoC, dc *cluster.DockerCLI, serverContainer string) error {
	cfg := bnkforge.Config{
		RepoPath:      p.BNKForge.RepoPath,
		URL:           p.BNKForge.URL,
		AdminUsername: p.BNKForge.AdminUsername,
		AdminPassword: p.BNKForge.AdminPassword,
	}.WithDefaults()
	if err := bnkforge.RequireRunning(ctx, cfg, out); err != nil {
		return err
	}
	cli := bnkforge.NewClient(cfg)
	if err := cli.Login(ctx, cfg.AdminUsername, cfg.AdminPassword); err != nil {
		return err
	}
	projectID, found, err := cli.FindProjectByName(ctx, p.Metadata.Name)
	if err != nil {
		return err
	}
	if !found {
		desc := fmt.Sprintf("Imported from ocibnkctl PoC %q (BNK %s).",
			p.Metadata.Name, p.Metadata.BNKVersion)
		color := p.BNKForge.ProjectColor
		if color == "" {
			color = "#0a3a5c"
		}
		projectID, err = cli.CreateProject(ctx, bnkforge.Project{
			Name:                  p.Metadata.Name,
			Description:           desc,
			ProjectType:           "kubernetes",
			CloudProvider:         "on-prem",
			Environment:           "dev",
			Region:                p.Metadata.Customer,
			TargetPlatformProfile: "generic_onprem",
			Color:                 color,
			Icon:                  p.BNKForge.ProjectIcon,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "      created bnk-forge project %q (id=%d)\n", p.Metadata.Name, projectID)
	}
	// Cluster registration with drift detection: when a cluster row
	// already exists for this PoC name, compare the stored apiserver
	// URL against the localized kubeconfig. If they differ, the row
	// is stale (e.g. destroy + redeploy with the same PoC name; the
	// k3s server's mapped apiserver port rotates on each create) —
	// DELETE the stale
	// row and POST a fresh one so bnk-forge talks to the new cluster.
	clusters, err := cli.ListProjectClusters(ctx, projectID)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(repo, "artifacts", "kubeconfig"))
	if err != nil {
		return err
	}
	localServer, err := bnkforge.KubeconfigAPIServer(body)
	if err != nil {
		return fmt.Errorf("read local kubeconfig: %w", err)
	}
	// On macOS/Windows the container runtime is a Linux VM (Docker Desktop /
	// podman machine). There the host-published https://127.0.0.1:<mapped-port>
	// in the kubeconfig is unreachable from inside bnk-forge's own containers
	// (their loopback is themselves), while separate Docker networks route
	// freely — so we rewrite the server to the k3s server container's network
	// IP, which bnk-forge can reach and which the apiserver cert lists as a SAN
	// (TLS still verifies). On native Linux the host-loopback path already
	// works (bnk-forge has always registered fine) AND Docker's default
	// inter-bridge isolation would BLOCK that container IP — so we leave the
	// kubeconfig untouched there. 6443 is k3s's in-container apiserver port.
	// Rewrite when the apiserver's host-loopback URL is unreachable from where
	// bnk-forge's containers run: on macOS/Windows (Docker Desktop VM) always,
	// and on Linux when ocibnkctl itself runs inside a container (BNK Forge
	// artifact) — there the k3s node network is reachable but the host loopback
	// is not. Native-Linux-on-host keeps the loopback path (and Docker's
	// inter-bridge isolation would block the container IP anyway).
	if runtime.GOOS != "linux" || cluster.InContainer() {
		serverIP, ipErr := dc.ContainerIP(ctx, serverContainer)
		if ipErr != nil {
			return fmt.Errorf("look up apiserver IP for bnk-forge: %w", ipErr)
		}
		localServer = fmt.Sprintf("https://%s:6443", serverIP)
		body, err = bnkforge.RewriteServerURL(body, localServer)
		if err != nil {
			return fmt.Errorf("rewrite kubeconfig server for bnk-forge: %w", err)
		}
		fmt.Fprintf(out, "      rewrote apiserver to %s (reachable from bnk-forge containers on %s)\n",
			localServer, runtime.GOOS)
	}
	for _, c := range clusters {
		if c.Name != p.Metadata.Name {
			continue
		}
		if localServer != "" && c.APIServer != "" && c.APIServer == localServer {
			fmt.Fprintf(out, "      cluster %q already registered (id=%d, kubeconfig matches %s)\n",
				p.Metadata.Name, c.ID, c.APIServer)
			return nil
		}
		fmt.Fprintf(out, "      cluster %q stored kubeconfig drifted (stored=%q local=%q) — refreshing registration (id=%d)\n",
			p.Metadata.Name, c.APIServer, localServer, c.ID)
		if err := cli.DeleteCluster(ctx, c.ID); err != nil {
			return fmt.Errorf("delete stale cluster row id=%d: %w", c.ID, err)
		}
		break
	}
	id, err := cli.CreateProjectCluster(ctx, projectID, bnkforge.Cluster{
		Name:             p.Metadata.Name,
		Kubeconfig:       base64.StdEncoding.EncodeToString(body),
		CloudProvider:    "on-prem",
		Region:           p.Metadata.Customer,
		DefaultNamespace: "default",
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "      registered cluster %q (id=%d). open %s\n",
		p.Metadata.Name, id, cfg.URL)
	return nil
}
