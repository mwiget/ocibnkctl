package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
	"github.com/mwiget/ocibnkctl/kit"
)

// The deploy phases are kit/platform/<phase>/install.sh (docs/agentic-kit-
// plan.md, Phase 4): the same scripts an operator or agent runs by hand on
// any cluster. ocibnkctl adds what is PoC-specific: the --yolo/--confirm
// gates, poc.yaml (values in, status out), cluster reachability, the
// journal, and its own version pins passed as the scripts' variables.

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Install the BNK platform (prereqs, FLO, CNEInstance)",
	}
	cmd.AddCommand(newDeployPrereqsCmd())
	cmd.AddCommand(newDeployFLOCmd())
	cmd.AddCommand(newDeployCNECmd())
	cmd.AddCommand(newDeployShrinkCmd())
	return cmd
}

// ---------- prereqs ----------

type deployPrereqsFlags struct {
	pocDir        string
	yolo          bool
	confirmDeploy string
}

func newDeployPrereqsCmd() *cobra.Command {
	f := &deployPrereqsFlags{}
	cmd := &cobra.Command{
		Use:   "prereqs",
		Short: "Install namespaces + cert-manager + FAR pull secret (DESTRUCTIVE)",
		Long: `Phase deploy-1: install everything BNK needs before the FLO chart.
Runs kit/platform/prereqs/install.sh (also usable by hand on any cluster):

  1. Tools preflight (kubectl, helm).
  2. Extract FAR pull secret from the tgz at bnk.far_key_ref.
  3. Ensure namespaces: f5-operators, f5-cne-core, default.
  4. Apply far-secret (kubernetes.io/dockerconfigjson) in each.
  5. helm install cert-manager (jetstack), wait for Available.

Required gates:
  --yolo                  acknowledge cluster writes
  --confirm-deploy NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployPrereqs(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge cluster writes")
	cmd.Flags().StringVar(&f.confirmDeploy, "confirm-deploy", "", "Must equal poc.yaml.metadata.name (typo guard)")
	return cmd
}

func runDeployPrereqs(ctx context.Context, out io.Writer, f *deployPrereqsFlags) error {
	repo, p, kubeconfig, err := loadDeployContext(ctx, f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy prereqs"); err != nil {
		return err
	}
	farPath := resolveRef(repo, p.BNK.FARKeyRef)
	if _, err := os.Stat(farPath); err != nil {
		return fmt.Errorf("FAR tgz not found at %s — drop the file there and retry", farPath)
	}
	fmt.Fprintf(out, "PoC:     %s\nCluster: %s\nFAR:     %s\n\n", p.Metadata.Name, kubeconfig, farPath)
	if err := runPlatform(ctx, out, "prereqs", platformEnv(repo, p, kubeconfig)); err != nil {
		return err
	}
	p.Status.Deploy = "in_progress"
	p.Status.LastPhaseAt = time.Now().UTC()
	if err := savePoC(repo, p, out); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nDONE.  Next: `ocibnkctl deploy flo`.")
	return nil
}

// ---------- flo ----------

type deployFLOFlags struct {
	pocDir        string
	yolo          bool
	confirmDeploy string
}

func newDeployFLOCmd() *cobra.Command {
	f := &deployFLOFlags{}
	cmd := &cobra.Command{
		Use:   "flo",
		Short: "Install F5 Lifecycle Operator + CWC API certs (DESTRUCTIVE)",
		Long: `Phase deploy-2: install FLO. Runs kit/platform/flo/install.sh:

  1. Pull release-manifest from repo.f5.com to resolve FLO + cert-gen
     chart versions for BNK ` + version.CNEManifestVersion + `.
  2. Inspect JWT (diagnostic — TEEM endpoint is derived from the JWT's
     jku at runtime by CWC).
  3. Apply bnk-ca cert-issuer chain.
  4. helm install FLO (manifest-resolved version).
  5. Wait for FLO controller Available.
  6. Generate + apply CWC API certs (gen_cert.sh; alpine/k8s when
     make/openssl/python3 are not installed locally).

Required gates:
  --yolo                  acknowledge cluster writes
  --confirm-deploy NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployFLO(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge cluster writes")
	cmd.Flags().StringVar(&f.confirmDeploy, "confirm-deploy", "", "Must equal poc.yaml.metadata.name (typo guard)")
	return cmd
}

func runDeployFLO(ctx context.Context, out io.Writer, f *deployFLOFlags) error {
	repo, p, kubeconfig, err := loadDeployContext(ctx, f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy flo"); err != nil {
		return err
	}
	farPath := resolveRef(repo, p.BNK.FARKeyRef)
	jwtPath := resolveRef(repo, p.BNK.JWTRef)
	for _, x := range []struct{ what, path string }{
		{"FAR tgz", farPath}, {"JWT", jwtPath},
	} {
		if _, err := os.Stat(x.path); err != nil {
			return fmt.Errorf("%s not found at %s — drop the file there and retry", x.what, x.path)
		}
	}
	fmt.Fprintf(out, "PoC:      %s\nCluster:  %s\nJWT:      %s\nManifest: %s\n\n",
		p.Metadata.Name, kubeconfig, jwtPath, version.CNEManifestVersion)
	if err := runPlatform(ctx, out, "flo", platformEnv(repo, p, kubeconfig)); err != nil {
		return err
	}
	// The script records the chart versions it resolved from the manifest.
	rel, err := readEnvFile(filepath.Join(repo, "artifacts", "release.env"))
	if err != nil {
		return fmt.Errorf("read the resolved chart versions: %w", err)
	}
	p.Versions.FLOChart = rel["FLO_CHART_VERSION"]
	p.Status.LastPhaseAt = time.Now().UTC()
	if err := savePoC(repo, p, out); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nDONE.  Next: `ocibnkctl deploy cne`.")
	return nil
}

// ---------- cne ----------

type deployCNEFlags struct {
	pocDir        string
	yolo          bool
	confirmDeploy string
}

func newDeployCNECmd() *cobra.Command {
	f := &deployCNEFlags{}
	cmd := &cobra.Command{
		Use:   "cne",
		Short: "Apply the data-plane prerequisites, CNEInstance, License, GatewayClass (DESTRUCTIVE)",
		Long: `Phase deploy-3: the BNK workload. Runs kit/platform/cne/install.sh:

  0. Data-plane prep for bnk.tmm_dataplane_mode (Multus, whereabouts,
     NADs, node prep, the OcNOS template).
  1. Apply the CNEInstance (demoMode, TMM DaemonSet on app=f5-tmm workers).
  2. Apply the License CR with the operator's JWT (skipped when Active).
  3. Wait for the License to become Active (or warn).
  4. GatewayClass (Accepted) and the Infra IPAM CR, then the mode's
     finishing steps and the TMM / workload readiness gates.

Required gates:
  --yolo                  acknowledge cluster writes
  --confirm-deploy NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployCNE(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge cluster writes")
	cmd.Flags().StringVar(&f.confirmDeploy, "confirm-deploy", "", "Must equal poc.yaml.metadata.name (typo guard)")
	return cmd
}

func runDeployCNE(ctx context.Context, out io.Writer, f *deployCNEFlags) error {
	repo, p, kubeconfig, err := loadDeployContext(ctx, f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy cne"); err != nil {
		return err
	}
	if _, err := readJWT(resolveRef(repo, p.BNK.JWTRef)); err != nil {
		return fmt.Errorf("read JWT: %w", err)
	}
	fmt.Fprintf(out, "PoC:     %s\nCluster: %s\n\n", p.Metadata.Name, kubeconfig)

	// host_profile normally comes from poc.yaml — `init` pins it to small on a
	// tight host. Safety net for a poc.yaml that predates that (or was created
	// on a roomier host / by hand): if it's still unset and this host is below
	// the core floor, resolve to small in-memory so TMM sheds its metrics
	// sidecar and fits. Non-destructive — poc.yaml is left untouched; set
	// host_profile=standard there to force the full footprint.
	if prof, autoSmall := p.BNK.ResolveHostProfile(runtime.NumCPU(), version.MinBaseline.Cores); autoSmall {
		p.BNK.HostProfile = prof
		fmt.Fprintf(out, "host has %d cores < %d-core floor and poc.yaml host_profile is unset — using host_profile=small (TMM metrics subsystem off)\n\n",
			runtime.NumCPU(), version.MinBaseline.Cores)
	}
	if err := runPlatform(ctx, out, "cne", platformEnv(repo, p, kubeconfig)); err != nil {
		return err
	}
	p.Status.Deploy = "ready"
	p.Status.LastPhaseAt = time.Now().UTC()
	if err := savePoC(repo, p, out); err != nil {
		return err
	}
	if j, jerr := appendJournal(repo, "deploy", "deploy cne — APPLIED"); jerr == nil {
		fmt.Fprintf(j, "- CNEInstance + License applied at %s\n", time.Now().UTC().Format(time.RFC3339))
		j.Close()
	}
	fmt.Fprintln(out, "\nDONE.")
	return nil
}

// ---------- shrink ----------

type deployShrinkFlags struct {
	pocDir        string
	yolo          bool
	confirmDeploy string
	cpu           string
	memory        string
	noRecycle     bool
}

func newDeployShrinkCmd() *cobra.Command {
	f := &deployShrinkFlags{}
	cmd := &cobra.Command{
		Use:   "shrink",
		Short: "Install a Kyverno policy that caps F5 pod resource requests (DESTRUCTIVE; auto-run by e2e on tight hosts)",
		Long: `Shrink the cluster's resource footprint so it fits a tight host.

Run standalone, or automatically: ` + "`e2e`" + ` inserts this as a phase
between deploy-flo and deploy-cne whenever the host is below the documented
core floor (and skips it on roomier hosts). On a host at/above the floor you
never need it.

The FLO operator owns every BNK workload spec via server-side-apply and
reasserts it on a tight reconcile loop, so resource requests cannot be
lowered by patching a Deployment or an intermediate F5* CR — FLO reverts it
within milliseconds. The only layer it can't reach is Kubernetes admission,
which runs AFTER FLO's apply. This step (kit/platform/shrink/install.sh):

  1. Installs Kyverno (admission controller only, pinned).
  2. Applies a mutating ClusterPolicy that caps CPU/memory *requests*
     (never limits) on every F5 pod EXCEPT f5-tmm.
  3. Caps the kube-system calico/multus DaemonSets directly (Kyverno
     excludes system namespaces) via a resource patch.
  4. Recycles the affected F5 pods so the cap takes effect now.

f5-tmm is excluded on purpose: its bespoke f5-tmm-pod-manager controller
deletes any TMM pod whose live spec differs from what FLO rendered, so a
mutated TMM pod loops forever. Shrink TMM's main container via
CNEInstance.spec.advanced.tmm.resources instead (that path goes through
FLO). Measured effect on the 2-node demo shape: server requests drop from
~89% CPU / 85% mem to ~9% / 21%; agent (TMM) from ~77% / 92% to ~45% / 58%.

Required gates:
  --yolo                  acknowledge cluster writes
  --confirm-deploy NAME   must equal poc.yaml.metadata.name`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeployShrink(cmd.Context(), cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.yolo, "yolo", false, "Acknowledge cluster writes")
	cmd.Flags().StringVar(&f.confirmDeploy, "confirm-deploy", "", "Must equal poc.yaml.metadata.name (typo guard)")
	cmd.Flags().StringVar(&f.cpu, "cpu", deploy.DefaultShrinkCPURequest, "Per-container CPU request ceiling")
	cmd.Flags().StringVar(&f.memory, "memory", deploy.DefaultShrinkMemoryRequest, "Per-container memory request ceiling")
	cmd.Flags().BoolVar(&f.noRecycle, "no-recycle", false, "Apply the policy but don't recycle pods (takes effect on next restart)")
	return cmd
}

func runDeployShrink(ctx context.Context, out io.Writer, f *deployShrinkFlags) error {
	repo, p, kubeconfig, err := loadDeployContext(ctx, f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy shrink"); err != nil {
		return err
	}
	fmt.Fprintf(out, "PoC:     %s\nCluster: %s\n\n", p.Metadata.Name, kubeconfig)
	env := append(platformEnv(repo, p, kubeconfig), "SHRINK_CPU="+f.cpu, "SHRINK_MEMORY="+f.memory)
	if f.noRecycle {
		env = append(env, "NO_RECYCLE=1")
	}
	if err := runPlatform(ctx, out, "shrink", env); err != nil {
		return err
	}
	if j, jerr := appendJournal(repo, "deploy", "deploy shrink — APPLIED"); jerr == nil {
		fmt.Fprintf(j, "- Kyverno shrink policy applied (cpu=%s mem=%s) at %s\n",
			f.cpu, f.memory, time.Now().UTC().Format(time.RFC3339))
		j.Close()
	}
	fmt.Fprintln(out, "\nDONE.  Verify per-node requests with:")
	fmt.Fprintln(out, "  kubectl describe node <node> | grep -A6 'Allocated resources:'")
	return nil
}

// ---------- the scripts ----------

// platformEnv is the PoC and ocibnkctl's pins as kit/platform variables
// (kit/platform/platform.env documents each; TestPlatformEnvMatchesGo keeps
// that file's defaults equal to these pins).
func platformEnv(repo string, p *poc.PoC, kubeconfig string) []string {
	aigw := ""
	if p.BNK.AIGateway.Enabled {
		aigw = "true"
	}
	helmHome := filepath.Join(repo, "artifacts", "helm-home")
	return []string{
		"KUBECONFIG=" + kubeconfig,
		"KIT_CALLER_DIR=" + repo,
		"KIT_CACHE=" + filepath.Join(repo, "artifacts", "bin"),
		"KIT_STATE=" + filepath.Join(repo, "artifacts"),
		// A per-PoC helm state dir, so the operator's own repo cache and
		// registry logins neither leak in nor get overwritten.
		"HELM_REPOSITORY_CONFIG=" + helmHome + "/repositories.yaml",
		"HELM_REPOSITORY_CACHE=" + helmHome + "/cache",
		"HELM_REGISTRY_CONFIG=" + helmHome + "/registry.json",
		"FAR_KEY=" + resolveRef(repo, p.BNK.FARKeyRef),
		"JWT_FILE=" + resolveRef(repo, p.BNK.JWTRef),
		"OPERATOR_NAMESPACE=f5-operators",
		"SHARED_NAMESPACE=" + deploy.SharedComponentNamespace,
		"BNK_NAMESPACE=default",
		"REGISTRY=" + version.FARRegistryHost,
		"MANIFEST_VERSION=" + version.CNEManifestVersion,
		"MANIFEST_CHART=" + version.ReleaseManifestRepo + "/" + version.ReleaseManifestChart,
		"FLO_CHART=" + version.FLOChartOCIRef,
		"CERT_GEN_CHART=oci://" + version.FARRegistryHost + "/utils/f5-cert-gen",
		"K8S_TOOLS_IMAGE=" + version.K8sToolsImage,
		"CERT_MANAGER_REPO=" + version.CertManagerRepo,
		"CERT_MANAGER_VERSION=" + version.CertManagerVersion,
		"KYVERNO_CHART=" + version.KyvernoChart,
		"KYVERNO_REPO=" + version.KyvernoRepo,
		"KYVERNO_VERSION=" + version.KyvernoVersion,
		"MULTUS_URL=" + deploy.MultusManifestURL,
		"MULTUS_SHA=" + deploy.MultusManifestSHA,
		"WHEREABOUTS_TAG=" + version.WhereaboutsTag,
		"WHEREABOUTS_DS_URL=" + version.WhereaboutsDaemonSetURL,
		"WHEREABOUTS_DS_SHA=" + version.WhereaboutsDaemonSetSHA,
		"WHEREABOUTS_IPPOOL_URL=" + version.WhereaboutsIPPoolCRDURL,
		"WHEREABOUTS_IPPOOL_SHA=" + version.WhereaboutsIPPoolCRDSHA,
		"WHEREABOUTS_RES_URL=" + version.WhereaboutsReservationCRDURL,
		"WHEREABOUTS_RES_SHA=" + version.WhereaboutsReservationCRDSHA,
		"DATAPLANE_MODE=" + p.BNK.DataplaneMode(),
		"TMM_WORKERS=" + strconv.Itoa(p.Cluster.Workers()),
		"EDGE_OCTET=" + strconv.Itoa(p.Cluster.EdgeNet()),
		"EDGE_PEER_IP=" + cluster.EdgeFRRIP(p.Cluster.EdgeNet()),
		"LOCAL_AS=" + strconv.Itoa(deploy.BGPTMMAS),
		"PEER_AS=" + strconv.Itoa(deploy.BGPPeerAS),
		"INSTANCE_NAME=bnk-instance",
		"DEPLOYMENT_SIZE=" + p.BNK.DeploymentSizeOrDefault(),
		"METRIC_SUBSYSTEM=" + strconv.FormatBool(p.BNK.MetricSubsystemEnabled()),
		"LOGGING_SUBSYSTEM=" + strconv.FormatBool(p.BNK.LoggingSubsystemEnabled()),
		"AIGW_ENABLED=" + aigw,
		"AIGW_METRICS=" + strconv.FormatBool(p.BNK.AIGateway.AIMetrics),
		"AIGW_CONSOLE_URL=" + p.BNK.AIGateway.ConsoleURL,
		"GATEWAYCLASS_NAME=" + deploy.GatewayClassName,
		// k3s nodes are containers: reach them through the runtime.
		"NODE_EXEC=" + p.Cluster.Provider + " exec",
	}
}

// runPlatform extracts kit/{lib,platform} to <PoC>/artifacts/kit (where the
// operator can rerun them) and runs platform/<phase>/install.sh with env,
// streaming its output.
func runPlatform(ctx context.Context, out io.Writer, phase string, env []string) error {
	var root string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "KIT_STATE="); ok {
			root = filepath.Join(v, "kit")
		}
	}
	for _, dir := range []string{"lib", "platform"} {
		if err := kit.Extract(dir, root); err != nil {
			return fmt.Errorf("extract kit/%s: %w", dir, err)
		}
	}
	script := filepath.Join(root, "platform", phase, "install.sh")
	fmt.Fprintf(out, "Running %s\n", script)
	w := prefixWriter{w: out, prefix: "      | "}
	cmd := exec.CommandContext(ctx, "sh", script)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("deploy %s: %s: %w", phase, filepath.Base(script), err)
	}
	return nil
}

// readEnvFile reads KEY=value lines.
func readEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			m[k] = v
		}
	}
	return m, nil
}

func loadDeployContext(ctx context.Context, pocDir string) (string, *poc.PoC, string, error) {
	repo, err := resolvePoCDir(pocDir)
	if err != nil {
		return "", nil, "", err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return "", nil, "", fmt.Errorf("not a PoC repo (%s): %w", repo, err)
	}
	kc, err := requireKubeconfig(repo, "run `ocibnkctl cluster up` first")
	if err != nil {
		return "", nil, "", err
	}
	// In-container preflight: attach to the cluster's docker network so the
	// kubeconfig's server-container-IP endpoint is reachable. No-op on the host,
	// idempotent, and required on an e2e resume where cluster-up is skipped.
	if err := cluster.EnsureReachable(ctx, p.Cluster.Provider, p.Cluster.Name); err != nil {
		return "", nil, "", fmt.Errorf("make cluster network reachable: %w", err)
	}
	return repo, p, kc, nil
}
