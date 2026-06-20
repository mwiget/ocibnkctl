package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
)

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
		Long: `Phase deploy-1: install everything BNK needs before the FLO chart:

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
	repo, p, kubeconfig, err := loadDeployContext(f.pocDir)
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

	r := &deploy.Runner{
		KubeconfigPath: kubeconfig,
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
	}

	fmt.Fprintf(out, "PoC:     %s\nCluster: %s\nFAR:     %s\n\n",
		p.Metadata.Name, kubeconfig, farPath)

	fmt.Fprintln(out, "[1/5] Tools preflight ...")
	if err := r.CheckTools(ctx); err != nil {
		return err
	}
	fmt.Fprintln(out, "      ok")

	fmt.Fprintln(out, "[2/5] Extracting FAR dockerconfigjson ...")
	dockerCfg, err := deploy.ExtractFARDockerConfig(farPath)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "      ok — %d bytes\n", len(dockerCfg))

	fmt.Fprintln(out, "[3/5] Ensuring namespaces ...")
	namespaces := []string{"f5-operators", deploy.SharedComponentNamespace, "default"}
	for _, ns := range namespaces {
		if ns != "default" {
			if err := r.Apply(ctx, deploy.RenderNamespace(ns)); err != nil {
				return err
			}
		}
	}
	fmt.Fprintln(out, "      ok")

	fmt.Fprintln(out, "[4/5] Applying far-secret in each namespace ...")
	for _, ns := range namespaces {
		if err := r.Apply(ctx, deploy.RenderFARSecret(ns, dockerCfg)); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "      ok")

	fmt.Fprintln(out, "[5/5] Installing cert-manager ...")
	if err := r.HelmUpgrade(ctx,
		"cert-manager",
		"cert-manager",
		version.CertManagerRepo,
		"cert-manager",
		version.CertManagerVersion,
		"installCRDs: true\n",
	); err != nil {
		return err
	}
	if err := r.Wait(ctx, "cert-manager", "Available", "deployment/cert-manager-webhook", 5*time.Minute); err != nil {
		return fmt.Errorf("cert-manager-webhook not Available: %w", err)
	}
	fmt.Fprintln(out, "      cert-manager Ready.")

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
		Long: `Phase deploy-2: install FLO:

  1. Pull release-manifest from repo.f5.com to resolve FLO + cert-gen
     chart versions for BNK ` + version.CNEManifestVersion + `.
  2. Inspect JWT (diagnostic — TEEM endpoint is derived from the JWT's
     jku at runtime by CWC).
  3. Apply bnk-ca cert-issuer chain.
  4. helm install FLO (manifest-resolved version).
  5. Wait for FLO controller Available.
  6. Generate + apply CWC API certs (gen_cert.sh in alpine/k8s).

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
	repo, p, kubeconfig, err := loadDeployContext(f.pocDir)
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

	r := &deploy.Runner{
		KubeconfigPath: kubeconfig,
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
	}

	fmt.Fprintf(out, "PoC:      %s\nCluster:  %s\nJWT:      %s\nManifest: %s\n\n",
		p.Metadata.Name, kubeconfig, jwtPath, version.CNEManifestVersion)

	// 1. Pull release-manifest.
	fmt.Fprintln(out, "[1/6] Pulling release-manifest from repo.f5.com ...")
	farAuth, err := deploy.ExtractFARRegistryAuth(farPath)
	if err != nil {
		return fmt.Errorf("extract FAR registry creds: %w", err)
	}
	mfCache := filepath.Join(repo, "artifacts", "release-manifest")
	manifest, err := deploy.PullReleaseManifest(ctx, farAuth, version.CNEManifestVersion, mfCache,
		filepath.Join(repo, "artifacts", "helm-home"))
	if err != nil {
		return fmt.Errorf("pull release-manifest: %w", err)
	}
	floVer := manifest.Chart("charts/f5-lifecycle-operator")
	certGenVer := manifest.Chart("utils/f5-cert-gen")
	if floVer == "" {
		return fmt.Errorf("release-manifest %s has no charts/f5-lifecycle-operator", manifest.Version)
	}
	if certGenVer == "" {
		return fmt.Errorf("release-manifest %s has no utils/f5-cert-gen", manifest.Version)
	}
	fmt.Fprintf(out, "      FLO chart    %s\n", floVer)
	fmt.Fprintf(out, "      f5-cert-gen  %s\n", certGenVer)
	p.Versions.FLOChart = floVer
	if err := savePoC(repo, p, out); err != nil {
		return err
	}

	// 2. JWT inspection.
	fmt.Fprintln(out, "[2/6] Inspecting JWT (diagnostic) ...")
	info, err := deploy.InspectJWT(jwtPath)
	if err != nil {
		return err
	}
	if _, err := readJWT(jwtPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "      type=%s  jku=%s  sub=%s\n", info.Type, info.JKU, redactJWTSub(info.Sub))

	// 3. bnk-ca cert-issuer chain.
	fmt.Fprintln(out, "[3/6] Applying bnk-ca cert-issuer chain ...")
	if err := r.Apply(ctx, deploy.CertIssuerChain()); err != nil {
		return err
	}
	if err := r.Wait(ctx, "cert-manager", "Ready", "certificate/bnk-ca", 3*time.Minute); err != nil {
		return fmt.Errorf("bnk-ca cert not ready: %w", err)
	}

	// 4. FLO helm install.
	fmt.Fprintln(out, "[4/6] Rendering FLO values + helm install ...")
	values, err := deploy.RenderFLOValues(deploy.FLOInputs{
		Namespace:                "f5-operators",
		SharedComponentNamespace: deploy.SharedComponentNamespace,
		ClusterIssuer:            "bnk-ca-cluster-issuer",
	})
	if err != nil {
		return err
	}
	rendered := filepath.Join(repo, "artifacts", "flo-values-rendered.yaml")
	if err := os.WriteFile(rendered, []byte(values), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "      rendered → %s\n", rendered)
	if err := r.HelmUpgradeOCI(ctx, farAuth,
		"flo", version.FLOChartOCIRef,
		"f5-operators", floVer, values,
	); err != nil {
		return err
	}

	// 5. Wait for FLO.
	fmt.Fprintln(out, "[5/6] Waiting for FLO controller Available ...")
	if err := r.Wait(ctx, "f5-operators", "Available", "deployment", 5*time.Minute,
		"-l", "app.kubernetes.io/name=f5-lifecycle-operator"); err != nil {
		fmt.Fprintf(out, "      WARN: FLO not Ready within 5min: %v\n", err)
	} else {
		fmt.Fprintln(out, "      FLO controller Ready.")
	}

	// 6. CWC API certs.
	fmt.Fprintln(out, "[6/6] Generating + applying CWC API certs ...")
	certsWork := filepath.Join(repo, "artifacts", "cwc-certs")
	if err := deploy.PullAndApplyCWCCerts(ctx, r, farAuth, certGenVer, certsWork,
		deploy.SharedComponentNamespace,
		prefixWriter{w: out, prefix: "      | "}); err != nil {
		return fmt.Errorf("CWC cert preflight: %w", err)
	}
	fmt.Fprintln(out, "      cwc-license-certs + cwc-license-client-certs applied.")

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
		Short: "Apply CNEInstance + License CR (DESTRUCTIVE)",
		Long: `Phase deploy-3: apply the workload custom resources:

  1. Apply CNEInstance (demoMode=true, TMM pinned via nodeSelector).
  2. Apply License CR with the operator's JWT.
  3. Wait for License to become Active (or warn).

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
	repo, p, kubeconfig, err := loadDeployContext(f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy cne"); err != nil {
		return err
	}
	jwtPath := resolveRef(repo, p.BNK.JWTRef)
	jwt, err := readJWT(jwtPath)
	if err != nil {
		return fmt.Errorf("read JWT: %w", err)
	}

	r := &deploy.Runner{
		KubeconfigPath: kubeconfig,
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
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

	// 0. All-active data-plane prep (opt-in via bnk.tmm_dataplane_mode).
	// Multus, the bridge CNI plugin, and the NAD(s) must all exist BEFORE
	// the CNEInstance attaches the NAD to TMM — otherwise the TMM pods get
	// stuck in sandbox creation ("failed to find plugin bridge"). All
	// steps are idempotent.
	switch {
	case p.BNK.IsSelfIPDAG():
		fmt.Fprintln(out, "[0/4] Active/active prep: Multus + bridge CNI + DAG NAD ...")
		if err := deploy.EnsureMultus(ctx, r); err != nil {
			return fmt.Errorf("active/active: ensure multus: %w", err)
		}
		namesOut, _ := r.KubectlCapture(ctx, "get", "nodes", "-o",
			`jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
		seedBridgeCNIPlugin(ctx, p.Cluster.Provider, strings.Fields(namesOut), out)
		if err := r.Apply(ctx, deploy.RenderDAGNAD("default")); err != nil {
			return fmt.Errorf("active/active: apply DAG NAD: %w", err)
		}
	case p.BNK.IsAnycastBGP():
		fmt.Fprintln(out, "[0/4] Anycast-BGP prep: Multus + bridge CNI + bnk-bgp NAD + FRR peer ...")
		if err := deploy.EnsureMultus(ctx, r); err != nil {
			return fmt.Errorf("anycast-bgp: ensure multus: %w", err)
		}
		namesOut, _ := r.KubectlCapture(ctx, "get", "nodes", "-o",
			`jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`)
		nodes := strings.Fields(namesOut)
		seedBridgeCNIPlugin(ctx, p.Cluster.Provider, nodes, out)
		// Packets on the bnk-bgp bridge must bypass the node's iptables, or
		// Calico's natOutgoing MASQUERADE breaks the data plane (see the
		// scenario's disableBridgeNetfilter rationale). Docker-only.
		disableBridgeNetfilter(ctx, p.Cluster.Provider, nodes, out)
		// TMM's NAD (host-local .20+) and FRR's static-IP NAD (.2), both on
		// br-bnk-bgp.
		if err := r.Apply(ctx, deploy.RenderBGPNAD("default")); err != nil {
			return fmt.Errorf("anycast-bgp: apply bnk-bgp NAD: %w", err)
		}
		if err := r.Apply(ctx, deploy.RenderFRRStaticNAD("default")); err != nil {
			return fmt.Errorf("anycast-bgp: apply FRR NAD: %w", err)
		}
		// FRR peer (one per app=f5-tmm node). Deployed now so it's
		// converging while TMM comes up.
		k, v := p.BNK.TMMLabel()
		if err := r.Apply(ctx, deploy.RenderFRRPeer("default", k, v)); err != nil {
			return fmt.Errorf("anycast-bgp: apply FRR peer: %w", err)
		}
	}

	// 1. CNEInstance.
	fmt.Fprintln(out, "[1/4] Rendering + applying CNEInstance (demoMode=true) ...")
	cne, err := deploy.RenderCNEInstance(p)
	if err != nil {
		return err
	}
	rendered := filepath.Join(repo, "artifacts", "cne-instance-rendered.yaml")
	if err := os.WriteFile(rendered, []byte(cne), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "      rendered → %s\n", rendered)
	if err := r.Apply(ctx, cne); err != nil {
		return err
	}

	// 2. License CR. The licenses.k8s.f5net.com CRD is installed by
	// FLO's crd-installer in response to the CNEInstance we just
	// applied — empirically, it doesn't exist until then. Wait for
	// both --for=create (CRD object exists) and condition=Established
	// (apiserver has bound the schema) before applying.
	fmt.Fprintln(out, "[2/4] Waiting for license CRD, then applying License CR ...")
	if err := r.Kubectl(ctx, "wait", "--for=create",
		"crd/licenses.k8s.f5net.com", "--timeout=3m"); err != nil {
		return fmt.Errorf("license CRD never created (FLO did not reconcile?): %w", err)
	}
	if err := r.Wait(ctx, "", "Established",
		"crd/licenses.k8s.f5net.com", 3*time.Minute); err != nil {
		return fmt.Errorf("license CRD did not become Established: %w", err)
	}
	cr, err := deploy.RenderLicenseCR(deploy.LicenseInputs{JWT: jwt})
	if err != nil {
		return err
	}
	// CWC creates a ResourceQuota (f5-single-license-quota) when it
	// reconciles License; for a brief window, `kubectl apply` fails
	// with "status unknown for quota". Retry up to ~100s.
	if err := applyLicenseWithQuotaRetry(ctx, r, cr, out); err != nil {
		return fmt.Errorf("apply License CR: %w", err)
	}

	// 3. Wait for License Active.
	fmt.Fprintln(out, "[3/4] Waiting for License Active ...")
	if err := deploy.WaitForLicenseActive(ctx, r,
		deploy.LicenseCRName, deploy.SharedComponentNamespace,
		20*time.Minute); err != nil {
		if strings.Contains(err.Error(), "PendingVerification") {
			fmt.Fprintf(out, "      WARN: license stuck at PendingVerification (disconnected mode? follow F5 docs to register manually)\n")
		} else {
			fmt.Fprintf(out, "      WARN: %v\n", err)
		}
	} else {
		fmt.Fprintln(out, "      License Active.")
	}

	// 4. Apply the cluster-scoped GatewayClass that every BNK Gateway
	//    references. This used to live in the http-routing-e2e scenario
	//    only, which meant any other scenario creating a Gateway BEFORE
	//    http-routing-e2e ran (which under topo-sorted --all is every
	//    Gateway-creating scenario) had its Gateway marked-for-deletion
	//    by f5-cne-controller — log line: "Not able to find gatewayClass
	//    object: bnk-gatewayclass". The GatewayClass is platform-level
	//    infrastructure, not test scaffolding, so it belongs here.
	//
	//    Block until f5-cne-controller has Accepted the GatewayClass —
	//    if we declare DONE before the controller picks it up, the very
	//    next workload to create a Gateway races the controller and the
	//    Gateway lands in "Pending: Waiting for controller" with no
	//    self-healing path. Treat the deploy as not-yet-successful until
	//    this prerequisite settles.
	fmt.Fprintln(out, "[4/5] Applying bnk-gatewayclass GatewayClass + waiting for Accepted=True ...")
	if err := r.Apply(ctx, `apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: bnk-gatewayclass
spec:
  controllerName: f5.com/default-f5-cne-controller
  description: "F5 BIG-IP Kubernetes Gateway managed by FLO"
`); err != nil {
		return fmt.Errorf("apply bnk-gatewayclass: %w", err)
	}
	if err := r.Wait(ctx, "", "Accepted",
		"gatewayclass/bnk-gatewayclass", 3*time.Minute); err != nil {
		return fmt.Errorf("bnk-gatewayclass never reached Accepted=True (f5-cne-controller not picking it up?): %w", err)
	}
	fmt.Fprintln(out, "      bnk-gatewayclass Accepted=True")

	// 5. Patch the f5-tmm Deployment rollout strategy.
	//
	// FLO ships the Deployment with RollingUpdate (maxSurge/maxUnavailable
	// 25%), which on the k3s worker runs two TMM pods on one node during
	// rollover — wasteful and prone to wedging Multus on veth churn.
	//
	//   - Single TMM (no HA goal): Recreate — terminate the old pod, then
	//     create the new one. No co-location, simplest.
	//   - Multiple TMMs: RollingUpdate, maxUnavailable=1, maxSurge=0 — roll
	//     ONE TMM at a time so N-1 stay serving across a reconfig. maxSurge
	//     MUST be 0: there's exactly one app=f5-tmm node per TMM, so a surge
	//     pod would either stay Pending (no free node) or stack two TMMs on
	//     one node fighting over net1 — the very churn Recreate avoided.
	//     With surge 0 the freed node is reused, one pod at a time (the
	//     scheduler's default same-ReplicaSet soft-spread keeps the new pod
	//     on the node just vacated).
	//
	// FLO does not reconcile the strategy back (verified, BNK 2.3); the
	// CNE/F5Tmm schema exposes no strategy knob, so a direct patch is the
	// only lever. Replacing the whole /spec/strategy works from any prior
	// state (Recreate has no rollingUpdate subfield to remove).
	strategyDesc := "Recreate"
	strategyPatch := `[{"op":"replace","path":"/spec/strategy","value":{"type":"Recreate"}}]`
	if p.Cluster.Workers() > 1 {
		strategyDesc = "RollingUpdate (maxUnavailable=1, maxSurge=0)"
		strategyPatch = `[{"op":"replace","path":"/spec/strategy","value":` +
			`{"type":"RollingUpdate","rollingUpdate":{"maxUnavailable":1,"maxSurge":0}}}]`
	}
	fmt.Fprintf(out, "[5/5] Patching f5-tmm Deployment strategy=%s ...\n", strategyDesc)
	if err := r.Kubectl(ctx, "-n", "default", "patch", "deployment", "f5-tmm",
		"--type=json", "-p", strategyPatch); err != nil {
		// Best-effort: a missing Deployment (rare race) or an
		// already-set strategy (idempotency) just makes noise.
		fmt.Fprintf(out, "      WARN: strategy patch failed: %v (continuing — scenarios still work, just blunter TMM rollovers)\n", err)
	} else {
		fmt.Fprintf(out, "      strategy=%s\n", strategyDesc)
	}

	// 6. All-active data-plane finalize. Done last because it needs the
	// rolled TMM pods up with net1 attached.
	switch {
	case p.BNK.IsSelfIPDAG():
		// Once TMM has net1 (mapres grabs it as interface "1.1"), program
		// the F5SPKVlan with one self-IP per TMM so each leaves standby and
		// becomes active, plus pod_hash for the stateless DAG.
		n := p.Cluster.Workers()
		selfIPs := deploy.DAGSelfIPs(n)
		fmt.Fprintf(out, "[6/6] Active/active: F5SPKVlan with %d self-IP(s) %v + pod_hash DAG ...\n", n, selfIPs)
		waitTMMNet1(ctx, r, deploy.DAGNADName, 3*time.Minute, out)
		if err := r.Apply(ctx, deploy.RenderTMMVlan("default", selfIPs)); err != nil {
			fmt.Fprintf(out, "      WARN: apply F5SPKVlan: %v (TMMs stay standby)\n", err)
		} else {
			fmt.Fprintf(out, "      %d TMM(s) active; each serves its own node's ingress\n", n)
		}
	case p.BNK.IsAnycastBGP():
		// Each TMM peers with its node-local FRR over net1 and advertises
		// its VIP /32 over BGP (anycast). Apply the cluster-wide ZeBOS
		// template (one neighbor = FRR's static IP, valid for every TMM
		// pod; router-id is the per-pod %%POD_IP%% token FLO expands), then
		// roll TMM ONCE so it loads the config, and inject passwd.conf into
		// every Running TMM pod (each runs its own ZeBOS session).
		n := p.Cluster.Workers()
		fmt.Fprintf(out, "[6/6] Anycast-BGP: ZeBOS template (neighbor %s) + roll %d TMM(s) + passwd.conf ...\n",
			deploy.BGPPeerIP, n)
		waitTMMNet1(ctx, r, deploy.BGPNADName, 3*time.Minute, out)
		// vip="" → advertise via `redistribute kernel` (FLO installs the
		// Gateway VIP /32 as a kernel route once a workload Gateway exists).
		if err := r.Apply(ctx, deploy.RenderAnycastZebosConfigMap("default", deploy.BGPPeerIP, "")); err != nil {
			fmt.Fprintf(out, "      WARN: apply ZeBOS ConfigMap: %v (TMMs won't peer)\n", err)
		} else {
			// One rollout restart so TMM reloads the routing template it now
			// mounts. Safe to re-run: FLO bakes the bnk-bgp NAD annotation
			// into the f5-tmm Deployment's pod template (from
			// CNEInstance.spec.networkAttachments), so restarted pods keep
			// net1 — unlike the bgp-peer-frr scenario's runtime-patch flow,
			// where the NAD isn't in the base spec and a bare restart drops
			// it (verified on a 2-node cluster, 2026-06-15).
			if err := r.Kubectl(ctx, "-n", "default", "rollout", "restart", "deployment/f5-tmm"); err != nil {
				fmt.Fprintf(out, "      WARN: rollout restart f5-tmm: %v\n", err)
			}
			// TMM pods roll one-at-a-time (maxUnavailable=1/maxSurge=0) and
			// each heavy pod takes a few minutes, so the status timeout must
			// scale with the node count or it spuriously trips on N>1.
			rolloutTimeout := time.Duration(4*(n+1)) * time.Minute
			if err := r.Kubectl(ctx, "-n", "default", "rollout", "status",
				"deployment/f5-tmm", "--timeout="+rolloutTimeout.String()); err != nil {
				fmt.Fprintf(out, "      WARN: f5-tmm rollout did not complete in %s: %v\n", rolloutTimeout, err)
			}
			waitTMMNet1(ctx, r, deploy.BGPNADName, 3*time.Minute, out)
			if err := deploy.InjectPasswdConfAll(ctx, r); err != nil {
				fmt.Fprintf(out, "      WARN: inject passwd.conf: %v (ZeBOS may not load until injected)\n", err)
			} else {
				// Each TMM peers with its node-local FRR and advertises its
				// connected/kernel routes; the Gateway VIP /32 appears once a
				// workload Gateway exists (redistribute kernel picks it up).
				fmt.Fprintf(out, "      %d TMM(s) peering with FRR at %s; each advertises its VIP /32 over BGP (anycast model)\n",
					n, deploy.BGPPeerIP)
			}
		}
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

// seedBridgeCNIPlugin copies each k3s node's own bundled bridge plugin
// (/bin/bridge, shipped by k3s) into /opt/cni/bin where Multus searches.
// k3s leaves only host-local/loopback/multus-shim there, so a bridge NAD
// otherwise fails sandbox creation with "failed to find plugin bridge".
// Using the node-local binary avoids any external download. Idempotent
// (skips nodes that already have it); best-effort per node.
func seedBridgeCNIPlugin(ctx context.Context, tool string, nodes []string, out io.Writer) {
	const script = `test -x /opt/cni/bin/bridge || ` +
		`(cp -f /bin/bridge /opt/cni/bin/bridge && chmod 0755 /opt/cni/bin/bridge)`
	for _, n := range nodes {
		if n == "" {
			continue
		}
		if err := exec.CommandContext(ctx, tool, "exec", n, "sh", "-c", script).Run(); err != nil {
			fmt.Fprintf(out, "      WARN: seed bridge CNI plugin on %s: %v\n", n, err)
		}
	}
}

// waitTMMNet1 blocks until at least one TMM pod reports nadName in its
// Multus network-status annotation (i.e. net1 attached), so whatever
// binds net1 (the F5SPKVlan's interface "1.1" for selfip-dag, or ZeBOS's
// update-source for anycast-bgp) has something to land on.
func waitTMMNet1(ctx context.Context, r *deploy.Runner, nadName string, timeout time.Duration, out io.Writer) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, _ := r.KubectlCapture(ctx, "-n", "default", "get", "pods", "-l", "app=f5-tmm",
			"-o", `jsonpath={range .items[*]}{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}{end}`)
		if strings.Contains(st, nadName) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	fmt.Fprintf(out, "      WARN: TMM net1 (%s) not observed within %s — net1-bound config may not apply until net1 is up\n",
		nadName, timeout)
}

// disableBridgeNetfilter sets net.bridge.bridge-nf-call-iptables=0 on
// every k3s node so packets crossing the bnk-bgp Multus bridge bypass
// the node's iptables — otherwise Calico's natOutgoing MASQUERADE SNATs
// FRR↔TMM data-plane traffic and breaks return symmetry over net1. Gated
// on whether br_netfilter is actually loaded (it is on Docker Desktop;
// on native-Linux docker the node container can't load the host module,
// so the sysctl is absent and there's nothing to disable). docker
// exec-only (podman unsupported — a pre-existing limitation carried over
// from the bgp-peer-frr scenario). Best-effort per node; idempotent.
func disableBridgeNetfilter(ctx context.Context, provider string, nodes []string, out io.Writer) {
	const sysctl = "/proc/sys/net/bridge/bridge-nf-call-iptables"
	for _, n := range nodes {
		if n == "" {
			continue
		}
		_ = exec.CommandContext(ctx, provider, "exec", n, "modprobe", "br_netfilter").Run()
		o, err := exec.CommandContext(ctx, provider, "exec", n, "sh", "-c",
			"if [ -e "+sysctl+" ]; then echo 0 > "+sysctl+" && echo set; else echo absent; fi").CombinedOutput()
		switch {
		case err != nil:
			fmt.Fprintf(out, "      WARN: bridge-nf-call-iptables on %s: %v\n", n, err)
		case strings.TrimSpace(string(o)) == "absent":
			fmt.Fprintf(out, "      bridge-nf-call-iptables on %s: absent (NAD bridge bypasses iptables)\n", n)
		default:
			fmt.Fprintf(out, "      bridge-nf-call-iptables=0 on %s\n", n)
		}
	}
}

// ---------- shrink (optional) ----------

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
which runs AFTER FLO's apply. This step:

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
	repo, p, kubeconfig, err := loadDeployContext(f.pocDir)
	if err != nil {
		return err
	}
	if err := requireTwoGates(f.yolo, "--confirm-deploy", f.confirmDeploy,
		p.Metadata.Name, "deploy shrink"); err != nil {
		return err
	}

	r := &deploy.Runner{
		KubeconfigPath: kubeconfig,
		HelmHome:       filepath.Join(repo, "artifacts", "helm-home"),
		Out:            prefixWriter{w: out, prefix: "      | "},
	}

	fmt.Fprintf(out, "PoC:     %s\nCluster: %s\n\n", p.Metadata.Name, kubeconfig)

	// 1. Kyverno (admission controller only).
	fmt.Fprintf(out, "[1/4] Installing Kyverno %s (admission controller only) ...\n", version.KyvernoVersion)
	if err := deploy.InstallKyverno(ctx, r); err != nil {
		return fmt.Errorf("install kyverno: %w", err)
	}

	// 2. Render + apply the mutating ClusterPolicy.
	fmt.Fprintf(out, "[2/4] Applying %s ClusterPolicy (cpu=%s mem=%s per container, f5-tmm excluded) ...\n",
		deploy.ShrinkPolicyName, f.cpu, f.memory)
	policy, err := deploy.RenderShrinkPolicy(deploy.ShrinkInputs{
		CPURequest:    f.cpu,
		MemoryRequest: f.memory,
	})
	if err != nil {
		return err
	}
	rendered := filepath.Join(repo, "artifacts", "kyverno-shrink-requests-rendered.yaml")
	if err := os.WriteFile(rendered, []byte(policy), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "      rendered → %s\n", rendered)
	if err := r.Apply(ctx, policy); err != nil {
		return fmt.Errorf("apply shrink policy: %w", err)
	}

	// 3. Cap kube-system (calico/multus) via a direct DaemonSet patch —
	//    Kyverno deliberately excludes system namespaces, and these have no
	//    operator to revert it. The ~0.35c freed on the TMM node is what
	//    lets the small-host (4-core) profile fit. Patching the DS template
	//    rolls those pods automatically.
	fmt.Fprintln(out, "[3/4] Capping kube-system DaemonSets (calico-node, kube-multus) ...")
	shrinkKubeSystem(ctx, r, f.cpu, f.memory, out)

	// 4. Recycle the F5 pods so the cap takes effect now (the policy only
	//    mutates pods at admission, i.e. on creation). f5-tmm is left alone.
	if f.noRecycle {
		fmt.Fprintln(out, "[4/4] Skipping F5 pod recycle (--no-recycle); cap applies as pods restart.")
	} else {
		fmt.Fprintln(out, "[4/4] Recycling F5 pods (f5-tmm excluded) so the cap takes effect ...")
		if err := recycleF5Pods(ctx, r, out); err != nil {
			return fmt.Errorf("recycle F5 pods: %w", err)
		}
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

// kubeSystemShrinkTargets are the kube-system DaemonSets capped by the
// shrink step via a direct patch (daemonset, container). Kyverno won't
// touch system namespaces, and these are plain manifest-installed
// DaemonSets with no operator to revert the change.
var kubeSystemShrinkTargets = []struct{ ds, container string }{
	{"calico-node", "calico-node"},
	{"kube-multus-ds", "kube-multus"},
}

// shrinkKubeSystem caps the calico/multus DaemonSet resource *requests*
// (never limits) via `kubectl set resources`. Best-effort: a missing
// target (e.g. a host running a different CNI) is logged and skipped, not
// fatal. Patching the DS template rolls its pods automatically.
func shrinkKubeSystem(ctx context.Context, r *deploy.Runner, cpu, memory string, out io.Writer) {
	for _, t := range kubeSystemShrinkTargets {
		if err := r.Kubectl(ctx, "-n", "kube-system", "set", "resources",
			"daemonset/"+t.ds, "--containers="+t.container,
			"--requests=cpu="+cpu+",memory="+memory); err != nil {
			fmt.Fprintf(out, "      WARN: cap %s/%s: %v (skipping)\n", t.ds, t.container, err)
		}
	}
}

// recycleF5Pods deletes the F5 pods so they re-admit through the shrink
// policy. f5-tmm is excluded (its pod-manager fights mutation); kube-system
// is handled separately by shrinkKubeSystem (a DS patch that self-rolls).
// Best-effort per namespace — a failure to delete one batch doesn't abort
// the rest.
func recycleF5Pods(ctx context.Context, r *deploy.Runner, out io.Writer) error {
	// Whole-namespace recycles: f5-operators / f5-cne-core hold only F5 workloads.
	for _, ns := range []string{"f5-operators", deploy.SharedComponentNamespace} {
		if err := r.Kubectl(ctx, "-n", ns, "delete", "pods", "--all", "--wait=false"); err != nil {
			fmt.Fprintf(out, "      WARN: delete pods -n %s: %v\n", ns, err)
		}
	}
	// default namespace: only f5-* pods, and NOT f5-tmm.
	names, err := r.KubectlCapture(ctx, "-n", "default", "get", "pods",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return err
	}
	for _, name := range strings.Split(strings.TrimSpace(names), "\n") {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, "f5-") || strings.HasPrefix(name, "f5-tmm-") {
			continue
		}
		if err := r.Kubectl(ctx, "-n", "default", "delete", "pod", name, "--wait=false"); err != nil {
			fmt.Fprintf(out, "      WARN: delete pod default/%s: %v\n", name, err)
		}
	}
	return nil
}

// applyLicenseWithQuotaRetry retries `kubectl apply` while the
// redactJWTSub trims a JWT subject claim down to "<type-prefix>-<4 chars>…"
// for diagnostic display. The full sub is a subscription/account
// identifier we don't want echoed verbatim into per-phase deploy logs
// that operators routinely paste into tickets / slack / GitHub. The
// prefix-plus-4 form keeps enough fingerprint to differentiate which
// JWT is loaded without leaking the full ID.
//
// "TST-EE4C16F4-7B16-463E-B050-0026A6E837E4" → "TST-EE4C…"
// "prod-account-12345"                       → "prod-acco…"
// ""                                         → ""
func redactJWTSub(s string) string {
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "-"); i > 0 && len(s) > i+5 {
		return s[:i+5] + "…"
	}
	if len(s) > 4 {
		return s[:4] + "…"
	}
	return s
}

// f5-single-license-quota's status.used is still unpopulated (CWC
// creates the quota at the same time it reconciles License, and the
// quota controller takes a moment to populate it). Bounded retry:
// 20 attempts × 5s = ~100s.
func applyLicenseWithQuotaRetry(ctx context.Context, r *deploy.Runner, manifest string, out io.Writer) error {
	const attempts = 20
	const interval = 5 * time.Second
	const quotaName = "f5-single-license-quota"
	var lastErr error
	for i := 1; i <= attempts; i++ {
		lastErr = r.Apply(ctx, manifest)
		if lastErr == nil {
			return nil
		}
		msg := lastErr.Error()
		if !strings.Contains(msg, "status unknown for quota") || !strings.Contains(msg, quotaName) {
			return lastErr
		}
		fmt.Fprintf(out, "      quota %s status not yet computed, retrying in %s (attempt %d/%d) ...\n",
			quotaName, interval, i, attempts)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("quota %s status never populated: %w", quotaName, lastErr)
}

// loadDeployContext resolves the PoC, loads it, and confirms the
// kubeconfig is present. Used by all three deploy subcommands.
func loadDeployContext(pocDir string) (string, *poc.PoC, string, error) {
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
	return repo, p, kc, nil
}
