package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/scenarios"
	"github.com/mwiget/ocibnkctl/internal/version"
	"github.com/mwiget/ocibnkctl/kit"
)

// render is Phase 0 of docs/agentic-kit-plan.md: write every manifest the
// deploy phases and scenarios would apply to plain files, laid out by phase,
// with a generated README giving the apply order and the exact helm/kubectl
// commands. No cluster, no network, no FAR key — the output is meant to be
// read, adapted and applied by hand (or by an agent) on any cluster.

type renderFlags struct {
	pocDir         string
	outDir         string
	includeSecrets bool
	force          bool
}

// renderMarker identifies a directory `render` created, so --force only ever
// clears a previous render and never an arbitrary directory.
const renderMarker = ".ocibnkctl-render"

func newRenderCmd() *cobra.Command {
	f := &renderFlags{}
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Write every BNK manifest + scenario as plain files with an apply guide (no cluster needed)",
		Long: `Render the manifests ocibnkctl would apply — cert-manager/FLO/CNE platform
objects and every scenario — into a directory of plain YAML, laid out by
phase, plus a README.md with the apply order and the exact helm/kubectl
commands. Use it to apply BNK by hand, to study what each phase does, or to
run scenarios on a cluster ocibnkctl did not build.

Values come from poc.yaml (dataplane mode, worker count, edge octet, host
profile). Secrets are NOT written unless --include-secrets is given: the FAR
pull secret and License CR are replaced by the commands that create them.

  ocibnkctl render --poc . --out ./bnk-manifests`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRender(cmd.OutOrStdout(), f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().StringVar(&f.outDir, "out", "", "Output directory (default: <poc>/artifacts/render)")
	cmd.Flags().BoolVar(&f.includeSecrets, "include-secrets", false, "Also write the FAR pull secret and License CR (JWT) — files are 0600")
	cmd.Flags().BoolVar(&f.force, "force", false, "Replace a previous render in --out")
	return cmd
}

func runRender(out io.Writer, f *renderFlags) error {
	repo, err := resolvePoCDir(f.pocDir)
	if err != nil {
		return err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
	}
	dest := f.outDir
	if dest == "" {
		dest = filepath.Join(repo, "artifacts", "render")
	}
	dest, err = filepath.Abs(expandTilde(dest))
	if err != nil {
		return err
	}
	if err := prepareRenderDir(dest, f.force); err != nil {
		return err
	}

	files, err := renderBundle(repo, p, f.includeSecrets)
	if err != nil {
		return err
	}
	for _, rf := range files {
		path := filepath.Join(dest, rf.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(rf.Body), rf.Mode); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dest, renderMarker), []byte(version.Version+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "Rendered %d file(s) for PoC %s (%s) → %s\n",
		len(files), p.Metadata.Name, p.BNK.DataplaneMode(), dest)
	fmt.Fprintf(out, "Start with %s\n", filepath.Join(dest, "README.md"))
	if !f.includeSecrets {
		fmt.Fprintln(out, "Secrets omitted: the FAR pull secret and License CR are described in the README (--include-secrets to write them).")
	}
	return nil
}

// prepareRenderDir refuses to write into a non-empty directory unless it is
// a previous render and --force is set, in which case that render is removed.
func prepareRenderDir(dest string, force bool) error {
	entries, err := os.ReadDir(dest)
	if os.IsNotExist(err) || (err == nil && len(entries) == 0) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(dest, renderMarker)); err != nil {
		return fmt.Errorf("%s is not empty and is not a previous render — choose another --out", dest)
	}
	if !force {
		return fmt.Errorf("%s holds a previous render — pass --force to replace it", dest)
	}
	return os.RemoveAll(dest)
}

// renderedFile is one file of the bundle, relative to the output root.
type renderedFile struct {
	Path string
	Body string
	Mode os.FileMode
}

type renderBuilder struct {
	files  []renderedFile
	readme strings.Builder
}

func (b *renderBuilder) add(path, body string) {
	b.files = append(b.files, renderedFile{Path: path, Body: body, Mode: 0o644})
}

func (b *renderBuilder) addSecret(path, body string) {
	b.files = append(b.files, renderedFile{Path: path, Body: body, Mode: 0o600})
}

func (b *renderBuilder) say(format string, a ...any) { fmt.Fprintf(&b.readme, format, a...) }

// addKitTree copies kit.FS's src directory to dst in the bundle; shell
// scripts stay executable.
func (b *renderBuilder) addKitTree(src, dst string) error {
	return fs.WalkDir(kit.FS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := kit.Read(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") && !strings.Contains(p, "/manifests/") {
			mode = 0o755 // scripts run by hand; .sh payloads under manifests/ are data
		}
		b.files = append(b.files, renderedFile{Path: dst + strings.TrimPrefix(p, src), Body: body, Mode: mode})
		return nil
	})
}

// yamlDocs joins manifests into one multi-document YAML stream.
func yamlDocs(docs ...string) string {
	for i, d := range docs {
		docs[i] = strings.TrimRight(d, "\n") + "\n"
	}
	return strings.Join(docs, "---\n")
}

// renderBundle builds the full render for p. The platform manifests come
// from the kit's own render_* functions (kit/platform/lib.sh), so they are
// byte for byte what the install scripts apply. Pure apart from reading the
// PoC's keys (only with includeSecrets), its cached release manifest, and
// scratch directories for the kit and scenario Manifests().
func renderBundle(repo string, p *poc.PoC, includeSecrets bool) ([]renderedFile, error) {
	k, err := newKitRenderer(repo, p)
	if err != nil {
		return nil, err
	}
	defer k.close()
	b := &renderBuilder{}
	floVer, certGenVer := resolvedChartVersions(repo, p)
	mode := p.BNK.DataplaneMode()
	shared := deploy.SharedComponentNamespace
	pullNamespaces := []string{"f5-operators", shared, "default"}

	b.say("# BNK %s manifests — PoC %s\n\n", version.BNKVersion, p.Metadata.Name)
	b.say("Rendered by `ocibnkctl render` (%s). Every file here is what `ocibnkctl deploy …`\n", version.Version)
	b.say("and `ocibnkctl scenario run …` would apply for this PoC; apply them in the order below\n")
	b.say("to install BNK by hand, on this cluster or any other.\n\n")
	b.say("| Setting | Value |\n|---|---|\n")
	b.say("| CNE release manifest | `%s` |\n", version.CNEManifestVersion)
	b.say("| FLO chart | %s |\n", orResolve(floVer))
	b.say("| f5-cert-gen chart | %s |\n", orResolve(certGenVer))
	b.say("| TMM dataplane mode | `%s` |\n", mode)
	b.say("| TMM workers (`app=f5-tmm`) | %d |\n", p.Cluster.Workers())
	b.say("| Host profile | `%s` |\n\n", orDefault(p.BNK.HostProfile, "standard"))
	b.say("Unresolved chart versions: run `ocibnkctl manifest probe` (needs only the FAR key), or\n")
	b.say("`helm pull %s/%s --version %s` and read the BOM.\n\n",
		version.ReleaseManifestRepo, version.ReleaseManifestChart, version.CNEManifestVersion)
	b.say("All commands assume `kubectl`/`helm` already point at the target cluster, and that you\n")
	b.say("have logged in to the F5 registry: see [registry login](#registry-login). On macOS use\n")
	b.say("`shasum -a 256 -c` for `sha256sum -c` and `sed -i ''` for `sed -i`.\n\n")
	b.say("The same steps also run as scripts: `platform/<phase>/install.sh` for prereqs, flo, shrink\n")
	b.say("and cne, with the variables in `platform/platform.env` (set them in the environment).\n")
	b.say("The files below are rendered by those scripts' own `render_*` functions (`platform/lib.sh`).\n\n")

	// ---- 00 prereqs ----
	b.say("## 00-prereqs\n\n")
	ns, err := k.manifest("render_namespaces")
	if err != nil {
		return nil, err
	}
	b.add("00-prereqs/01-namespaces.yaml", ns)
	b.say("```sh\nkubectl apply -f 00-prereqs/01-namespaces.yaml\n```\n\n")
	b.say("**FAR pull secret** `far-secret` in %s. The FAR tarball holds a base64 GCP\n",
		backtickJoin(pullNamespaces))
	b.say("service-account key (`cne_pull_64.json`); the registry user is `_json_key_base64`:\n\n")
	if includeSecrets {
		far, err := k.manifest("render_far_secrets")
		if err != nil {
			return nil, fmt.Errorf("--include-secrets: %w", err)
		}
		b.addSecret("00-prereqs/02-far-secret.yaml", far)
		b.say("```sh\nkubectl apply -f 00-prereqs/02-far-secret.yaml   # contains your FAR credential\n```\n\n")
	} else {
		b.say("```sh\ntar -xzf %s   # → cne_pull_64.json\nfor ns in %s; do\n", p.BNK.FARKeyRef, strings.Join(pullNamespaces, " "))
		b.say("  kubectl -n $ns create secret docker-registry far-secret --docker-server=%s \\\n", version.FARRegistryHost)
		b.say("    --docker-username=_json_key_base64 --docker-password=\"$(tr -d '\\n' < cne_pull_64.json)\" \\\n")
		b.say("    --dry-run=client -o yaml | kubectl apply -f -\ndone\n```\n\n")
	}
	b.say("**cert-manager** %s:\n\n```sh\n", version.CertManagerVersion)
	b.say("helm upgrade --install cert-manager cert-manager --repo %s \\\n", version.CertManagerRepo)
	b.say("  --namespace cert-manager --create-namespace --version %s --set installCRDs=true\n", version.CertManagerVersion)
	b.say("kubectl -n cert-manager wait --for=condition=Available deployment/cert-manager-webhook --timeout=5m\n```\n\n")

	// ---- 10 FLO ----
	b.say("## 10-flo\n\n")
	issuer, err := kit.Read("platform/flo/bnk-ca-issuer.yaml")
	if err != nil {
		return nil, err
	}
	b.add("10-flo/01-bnk-ca-issuer.yaml", issuer)
	values, err := k.manifest("render_flo_values")
	if err != nil {
		return nil, err
	}
	b.add("10-flo/02-flo-values.yaml", values)
	b.say("```sh\nkubectl apply -f 10-flo/01-bnk-ca-issuer.yaml\n")
	b.say("kubectl -n cert-manager wait --for=condition=Ready certificate/bnk-ca --timeout=3m\n\n")
	b.say("helm upgrade --install flo %s --version %s \\\n", version.FLOChartOCIRef, orPlaceholder(floVer, "FLO_CHART_VERSION"))
	b.say("  --namespace f5-operators --create-namespace -f 10-flo/02-flo-values.yaml\n")
	b.say("kubectl -n f5-operators wait --for=condition=Available deployment \\\n")
	b.say("  -l app.kubernetes.io/name=f5-lifecycle-operator --timeout=5m\n```\n\n")
	b.say("**CWC API certs.** `gen_cert.sh` from the f5-cert-gen chart needs `make`, `openssl` and\n")
	b.say("`python3` (or run it inside `%s`):\n\n```sh\n", version.K8sToolsImage)
	b.say("helm pull oci://%s/utils/f5-cert-gen --version %s --untar\n",
		version.FARRegistryHost, orPlaceholder(certGenVer, "CERT_GEN_CHART_VERSION"))
	b.say("sh cert-gen/gen_cert.sh -s=api-server -a=f5-spk-cwc.%s.svc.cluster.local -n=1\n", shared)
	b.say("kubectl -n %s apply -f cwc-license-certs.yaml -f cwc-license-client-certs.yaml\n```\n\n", shared)

	// ---- 15 shrink (optional) ----
	policy, err := k.manifest("render_shrink_policy")
	if err != nil {
		return nil, err
	}
	b.add("15-shrink/01-kyverno-shrink-requests.yaml", policy)
	b.say("## 15-shrink (optional)\n\n")
	b.say("Only for hosts below the resource floor (`ocibnkctl doctor`). FLO reasserts every workload\n")
	b.say("spec, so requests are capped at admission by Kyverno, not in chart values. Must be admitting\n")
	b.say("**before** 20-cne creates the TMM/DSSM pods.\n\n```sh\n")
	b.say("helm upgrade --install kyverno %s --repo %s --namespace kyverno --create-namespace \\\n",
		version.KyvernoChart, version.KyvernoRepo)
	b.say("  --version %s --set backgroundController.enabled=false --set cleanupController.enabled=false \\\n", version.KyvernoVersion)
	b.say("  --set reportsController.enabled=false --set admissionController.replicas=1\n")
	b.say("kubectl -n kyverno wait --for=condition=Available deployment/kyverno-admission-controller --timeout=5m\n")
	b.say("kubectl apply -f 15-shrink/01-kyverno-shrink-requests.yaml\n```\n\n")
	if p.BNK.IsSmallHost() {
		b.say("This PoC uses `host_profile: small`, which assumes this step.\n\n")
	}

	// ---- 20 CNE ----
	if err := renderCNE(b, k, repo, p, includeSecrets); err != nil {
		return nil, err
	}

	// ---- scenarios ----
	if err := renderScenarios(b, p); err != nil {
		return nil, err
	}

	b.say("## Registry login\n\n")
	b.say("`helm` pulls from `oci://%s` need a login with the FAR credential:\n\n", version.FARRegistryHost)
	b.say("```sh\ntr -d '\\n' < cne_pull_64.json | helm registry login %s --username _json_key_base64 --password-stdin\n```\n",
		version.FARRegistryHost)

	for _, dir := range []string{"lib", "platform"} {
		if err := b.addKitTree(dir, dir); err != nil {
			return nil, err
		}
	}
	b.say("\n`lib/render.sh` fills the `${NAME}` placeholders in the `.tmpl` files from environment\n")
	b.say("variables (POSIX sh + awk, also on macOS); see its header. The other `lib/` files are the\n")
	b.say("helpers the scenario scripts source; `platform/` holds the install scripts and their templates.\n")

	b.add("README.md", b.readme.String())
	return b.files, nil
}

func renderCNE(b *renderBuilder, k *kitRenderer, repo string, p *poc.PoC, includeSecrets bool) error {
	shared := deploy.SharedComponentNamespace
	b.say("## 20-cne\n\n")
	n := 0
	next := func(name string) string {
		n++
		return fmt.Sprintf("20-cne/%02d-%s.yaml", n, name)
	}
	add := func(path, fn string) error {
		body, err := k.manifest(fn)
		if err == nil {
			b.add(path, body)
		}
		return err
	}

	if p.BNK.IsAllActive() {
		patches, err := k.manifest("multus_patches 100m 200Mi")
		if err != nil {
			return err
		}
		shrink, err := k.manifest(`printf 'cpu %s, memory %s' "$SHRINK_CPU" "$SHRINK_MEMORY"`)
		if err != nil {
			return err
		}
		b.say("**Multus** (thick plugin) — download, check, apply, then the two fixes `deploy cne` patches in\n")
		b.say("(rename-based shim install against the restart deadlock; 500Mi limit against OOMKills):\n\n```sh\n")
		b.say("curl -fsSLo multus.yaml %s\n", deploy.MultusManifestURL)
		b.say("echo '%s  multus.yaml' | sha256sum -c -\n", deploy.MultusManifestSHA)
		b.say("kubectl apply -f multus.yaml\n")
		for _, patch := range strings.Split(strings.TrimSpace(patches), "\n") {
			b.say("kubectl -n kube-system patch daemonset/kube-multus-ds --type=json -p '%s'\n", patch)
		}
		b.say("kubectl -n kube-system rollout status daemonset/kube-multus-ds --timeout=3m\n```\n\n")
		b.say("With 15-shrink applied, use its requests (%s) in the second patch.\n\n", shrink)
		b.say("Every node also needs the `bridge` CNI plugin in `/opt/cni/bin` (k3s ships it as `/bin/bridge`;\n")
		b.say("`deploy cne` copies it there on each node).\n\n")
	}

	switch {
	case p.BNK.IsAnycastBGP():
		peerIP := cluster.EdgeFRRIP(p.Cluster.EdgeNet())
		b.say("**Whereabouts** cluster-wide IPAM, so every TMM gets a unique net1 from one pool:\n\n```sh\n")
		for _, m := range []struct{ file, url, sha string }{
			{"whereabouts-ippools.yaml", version.WhereaboutsIPPoolCRDURL, version.WhereaboutsIPPoolCRDSHA},
			{"whereabouts-reservations.yaml", version.WhereaboutsReservationCRDURL, version.WhereaboutsReservationCRDSHA},
			{"whereabouts-ds.yaml", version.WhereaboutsDaemonSetURL, version.WhereaboutsDaemonSetSHA},
		} {
			b.say("curl -fsSLo %s %s\necho '%s  %s' | sha256sum -c -\n", m.file, m.url, m.sha, m.file)
		}
		b.say("sed -i 's|whereabouts:latest|whereabouts:%s|' whereabouts-ds.yaml   # upstream ships :latest\n", version.WhereaboutsTag)
		b.say("kubectl apply -f whereabouts-ippools.yaml -f whereabouts-reservations.yaml -f whereabouts-ds.yaml\n")
		b.say("kubectl -n kube-system rollout status daemonset/whereabouts --timeout=3m\n```\n\n")
		b.say("On each node set `net.bridge.bridge-nf-call-iptables=0`, or Calico's natOutgoing MASQUERADE\n")
		b.say("breaks traffic on the NAD bridge.\n\n")

		nad, ocnos := next("nad-bnk-bgp"), next("ocnos-template")
		if err := add(nad, "render_bgp_nad"); err != nil {
			return err
		}
		if err := add(ocnos, "render_ocnos"); err != nil {
			return err
		}
		b.say("The `%s` NAD puts TMM's net1 on bridge `br-bnk-bgp`, which must reach your BGP peer at L2.\n", deploy.BGPNADName)
		b.say("The OcNOS template peers **%s** (AS %d) from TMM AS %d — edit `neighbor` for your router.\n",
			peerIP, deploy.BGPPeerAS, deploy.BGPTMMAS)
		b.say("Apply both **before** the CNEInstance so TMM boots with the final routing config:\n\n")
		b.say("```sh\nkubectl apply -f %s -f %s\n```\n\n", nad, ocnos)
	case p.BNK.IsSelfIPDAG():
		nad := next("nad-dag")
		if err := add(nad, "render_dag_nad"); err != nil {
			return err
		}
		b.say("```sh\nkubectl apply -f %s\n```\n\n", nad)
	}

	cneFile := next("cne-instance")
	if err := add(cneFile, "render_cne"); err != nil {
		return err
	}
	b.say("**CNEInstance** — FLO installs the BNK CRDs (License among them) in response to it:\n\n")
	b.say("```sh\nkubectl apply -f %s\n", cneFile)
	b.say("kubectl wait --for=create crd/licenses.k8s.f5net.com --timeout=8m   # first pull of the manifest can be slow\n")
	b.say("kubectl wait --for=condition=Established crd/licenses.k8s.f5net.com --timeout=3m\n```\n\n")

	b.say("**License** — skip if `kubectl -n %s get license.k8s.f5net.com %s` already reports Active: re-applying\n",
		shared, deploy.LicenseCRName)
	b.say("makes CWC re-register, which fails on an already-registered asset. Retry the apply for ~100s if it\n")
	b.say("fails with \"status unknown for quota\" (CWC is still creating its ResourceQuota).\n\n")

	// Same rule as `deploy cne`: a tst JWT needs TEEM's current tst JWKS
	// (CWC's built-in tst certs expired). Only the JWT's claims are read
	// here; the token itself is written only with --include-secrets.
	// The ConfigMap shares the License's step number (and sorts first), so
	// adding it leaves every other file's number unchanged.
	licFile := next("license")
	jwksFile := strings.TrimSuffix(licFile, ".yaml") + "-jwks-configmap.yaml"
	if err := add(jwksFile, "render_jwks_configmap"); err != nil {
		return err
	}
	jwtType, err := k.manifest("jwt_type")
	if err != nil {
		return err
	}
	jwks := ""
	switch strings.TrimSpace(jwtType) {
	case "tst":
		jwks = deploy.JWKSConfigMapName
		b.say("This PoC's JWT is a **test** JWT, so the License points CWC at TEEM's current test JWKS\n")
		b.say("(`%s`, apply it first). Production JWTs need neither.\n\n", jwksFile)
	case "prod":
		b.say("This PoC's JWT is a production JWT: skip `%s`, which only test JWTs need.\n\n", jwksFile)
	default:
		b.say("For a **test** JWT (`jku` on `product-tst…`): apply `%s` first and add to the License spec\n", jwksFile)
		b.say("`jwksConfigMapRef: {name: %s, key: %s}` — CWC's built-in test certs expired. Production JWTs\n",
			deploy.JWKSConfigMapName, deploy.JWKSConfigMapKey)
		b.say("need neither.\n\n")
	}
	b.say("```sh\n")
	if jwks != "" {
		b.say("kubectl apply -f %s\n", jwksFile)
	}
	if includeSecrets {
		jwt, err := readJWT(resolveRef(repo, p.BNK.JWTRef))
		if err != nil {
			return fmt.Errorf("--include-secrets: %w", err)
		}
		cr, err := k.manifest("render_license "+jwks, "JWT="+jwt)
		if err != nil {
			return err
		}
		b.addSecret(licFile, cr)
		b.say("kubectl apply -f %s   # contains your JWT\n", licFile)
	} else {
		cr, err := k.manifest("render_license "+jwks, "JWT=${JWT}")
		if err != nil {
			return err
		}
		// .tmpl suffix keeps `kubectl apply -f 20-cne/` from applying the placeholder.
		licFile += ".tmpl"
		b.add(licFile, cr)
		b.say("JWT=\"$(tr -d '\\n' < %s)\" sh lib/render.sh %s | kubectl apply -f -\n", p.BNK.JWTRef, licFile)
	}
	b.say("kubectl -n %s wait --for=jsonpath='{.status.state}'=Active license.k8s.f5net.com/%s --timeout=20m\n```\n\n",
		shared, deploy.LicenseCRName)

	gc := next("gatewayclass")
	if err := add(gc, "render_gatewayclass"); err != nil {
		return err
	}
	b.say("**GatewayClass** — wait for Accepted before creating any Gateway, or the Gateway can stick at\n")
	b.say("\"Waiting for controller\":\n\n```sh\nkubectl apply -f %s\n", gc)
	b.say("kubectl wait --for=condition=Accepted gatewayclass/%s --timeout=5m\n```\n\n", deploy.GatewayClassName)

	if p.BNK.IsSelfIPDAG() {
		vlan := next("f5spkvlan")
		if err := add(vlan, "render_f5spkvlan"); err != nil {
			return err
		}
		b.say("**F5SPKVlan** (once every TMM has net1) — one self-IP per TMM. No Infra CR in this mode:\n")
		b.say("the legacy SPK network model never reconciles it.\n\n")
		b.say("```sh\nkubectl apply -f %s\n", vlan)
		b.say("kubectl -n default wait --for=condition=Programmed f5-spk-vlans/f5-tmm-dag --timeout=2m\n```\n\n")
	} else {
		infra := next("infra")
		if err := add(infra, "render_infra"); err != nil {
			return err
		}
		b.say("**Infra** — the IPAM pool `bnk-dynamic-vips` (203.0.113.110–203.0.113.119) for address-less\n")
		b.say("Gateways. Retry if the `f5-single-infra-quota` is not populated yet:\n\n")
		b.say("```sh\nkubectl apply -f %s\n", infra)
		b.say("kubectl -n default wait --for=condition=Programmed infras.gateway.k8s.f5.com/infra --timeout=3m\n```\n\n")
	}

	if p.BNK.IsAnycastBGP() {
		b.say("**BGP bring-up** on every running TMM, once `daemonset/f5-tmm` has rolled out (what\n")
		b.say("`bnk_ocnos_passwd` and `bnk_ocnos_redistribute` in `lib/bnk.sh` do):\n\n```sh\n")
		b.say("for pod in $(kubectl -n default get pod -l app=f5-tmm --field-selector=status.phase=Running -o name); do\n")
		b.say("  kubectl -n default exec -i $pod -c f5-tmm-routing -- sh -c 'cat > /config/zebos/rd0/passwd.conf' < lib/ocnos-passwd.conf\n")
		b.say("  LOCAL_AS=%d sh lib/render.sh lib/ocnos-redistribute.cfg |\n", deploy.BGPTMMAS)
		b.say("    kubectl -n default exec -i $pod -c f5-tmm-routing -- sh -c 'cat > /tmp/redist.cfg; imish -f /tmp/redist.cfg'\n")
		b.say("done\n```\n\n")
		b.say("OcNOS only advertises once `redistribute` is re-issued at runtime, hence the second exec.\n\n")
	}
	b.say("Done when `daemonset/f5-tmm` is ready on every `app=f5-tmm` node and every workload in\n")
	b.say("`%s` is Available.\n\n", shared)
	return nil
}

// renderScenarios writes each scenario's manifests (via its own Manifests,
// into a scratch PoC dir) plus a README carrying its metadata.
func renderScenarios(b *renderBuilder, p *poc.PoC) error {
	ordered, err := topoSortByDeps(scenarios.All())
	if err != nil {
		return err
	}
	scratch, err := os.MkdirTemp("", "ocibnkctl-render-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	b.say("## scenarios\n\n")
	b.say("One directory per scenario, in dependency order; each one's README says what it checks. Most\n")
	b.say("assume the `%s` GatewayClass and VIPs in 203.0.113.0/24 advertised to a BGP peer —\n", deploy.GatewayClassName)
	b.say("adapt them for another cluster.\n\n")
	b.say("- **script** scenarios run by hand with `sh scenarios/<name>/apply.sh`, then `verify.sh`\n")
	b.say("  (one JSON line per check) and `cleanup.sh`. With no edge client, run verify with\n")
	b.say("  `EDGE_DRIVER=none` and the data-plane checks are skipped; `lib/edge.sh` explains how to\n")
	b.say("  point it at your own client and BGP router.\n")
	b.say("- **manifests** scenarios: `kubectl apply -f scenarios/<name>/manifests/` (files apply in name\n")
	b.say("  order), `kubectl delete -f` the same to remove; the checks live in `ocibnkctl` for now.\n\n")
	b.say("`ocibnkctl scenario run` runs the data-plane checks from inside the external FRR container\n")
	b.say("(`bnk-edge-frr-%s`), which learns the VIPs from TMM over BGP.\n\n", p.Cluster.Name)
	b.say("| Scenario | Rating | Form | Depends on | Title |\n|---|---|---|---|---|\n")

	ctx := &scenarios.Context{PoC: p, PoCDir: scratch, DryRun: true, Out: io.Discard}
	for _, s := range ordered {
		dir := "scenarios/" + s.Name()
		form := "manifests"
		if ks, ok := s.(interface{ KitDir() string }); ok {
			// A script scenario is copied whole: its README, scripts,
			// scenario.env and manifests/, exactly as in kit/.
			form = "script"
			if err := b.addKitTree(ks.KitDir(), dir); err != nil {
				return err
			}
		} else {
			paths, err := s.Manifests(ctx)
			if err != nil {
				return fmt.Errorf("scenario %s: render manifests: %w", s.Name(), err)
			}
			sort.Strings(paths)
			var names []string
			for _, src := range paths {
				body, err := os.ReadFile(src)
				if err != nil {
					return err
				}
				name := filepath.Base(src)
				names = append(names, name)
				b.add(dir+"/manifests/"+name, string(body))
			}
			b.add(dir+"/README.md", scenarioReadme(s, names))
		}
		deps := strings.Join(s.Dependencies(), ", ")
		b.say("| [%s](%s/README.md) | %s | %s | %s | %s |\n", s.Name(), dir, s.Rating(), form, orDefault(deps, "—"), s.Title())
	}
	b.say("\n")
	return nil
}

func scenarioReadme(s scenarios.Scenario, manifests []string) string {
	var r strings.Builder
	fmt.Fprintf(&r, "# %s\n\n%s\n\n", s.Name(), s.Title())
	fmt.Fprintf(&r, "- Rating: **%s**\n", s.Rating())
	if deps := s.Dependencies(); len(deps) > 0 {
		fmt.Fprintf(&r, "- Depends on: %s (apply those first)\n", backtickJoin(deps))
	}
	fmt.Fprintf(&r, "\n## What it does\n\n%s\n\n## Manifests\n\n", strings.TrimSpace(s.Description()))
	if len(manifests) == 0 {
		r.WriteString("None — this scenario acts imperatively (e.g. exec into TMM or the BGP peer);\n")
		r.WriteString("the steps are in the description above.\n")
	} else {
		for _, m := range manifests {
			fmt.Fprintf(&r, "- `manifests/%s`\n", m)
		}
		r.WriteString("\nNot every file is applied unconditionally by `ocibnkctl scenario run`; check the\n")
		r.WriteString("description above for ordering and waits between steps.\n")
		for _, m := range manifests {
			if strings.HasSuffix(m, ".tmpl") {
				fmt.Fprintf(&r, "\n`manifests/%s` holds `${NAME}` placeholders that `ocibnkctl` fills from live\n", m)
				r.WriteString("cluster state at apply time (e.g. a Service ClusterIP). From this directory, fill them with\n")
				fmt.Fprintf(&r, "`NAME=<value> sh ../../lib/render.sh manifests/%s | kubectl apply -f -`. `kubectl apply\n", m)
				r.WriteString("-f` on the directory skips `.tmpl` files.\n")
			}
		}
	}
	return r.String()
}

// resolvedChartVersions returns the FLO and f5-cert-gen chart versions from
// the release manifest a previous `deploy flo` cached, falling back to the
// FLO version recorded in poc.yaml. Empty means unresolved (offline render).
func resolvedChartVersions(repo string, p *poc.PoC) (flo, certGen string) {
	flo = p.Versions.FLOChart
	body, err := os.ReadFile(filepath.Join(repo, "artifacts", "release-manifest", "manifest.yaml"))
	if err != nil {
		return flo, ""
	}
	m, err := deploy.ParseReleaseManifest(body)
	if err != nil || m.Version != version.CNEManifestVersion {
		return flo, ""
	}
	if v := m.Chart("charts/f5-lifecycle-operator"); v != "" {
		flo = v
	}
	return flo, m.Chart("utils/f5-cert-gen")
}

func orResolve(v string) string {
	if v == "" {
		return "_unresolved — see below_"
	}
	return "`" + v + "`"
}

func orPlaceholder(v, name string) string {
	if v == "" {
		return "${" + name + "}"
	}
	return v
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func backtickJoin(xs []string) string {
	q := make([]string, len(xs))
	for i, x := range xs {
		q[i] = "`" + x + "`"
	}
	return strings.Join(q, ", ")
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
