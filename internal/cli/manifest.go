package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/version"
)

// newManifestCmd groups the release-manifest (BOM) helpers. Unlike every
// other command that talks to repo.f5.com, these need no cluster and no
// kubeconfig — just the FAR key — so they can run before `cluster up`,
// which is the whole point: verifying a candidate CNEManifestVersion is
// a prerequisite for bumping the pin, not a consequence of deploying it.
func newManifestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Inspect the BNK release-manifest (bill of materials) on repo.f5.com",
	}
	cmd.AddCommand(newManifestProbeCmd())
	return cmd
}

type manifestProbeFlags struct {
	pocDir  string
	farPath string
	out     string
	all     bool
}

func newManifestProbeCmd() *cobra.Command {
	f := &manifestProbeFlags{}
	cmd := &cobra.Command{
		Use:   "probe [manifest-version]",
		Short: "Pull a release-manifest from repo.f5.com and print its BOM (no cluster required)",
		Long: `Pull the f5-bigip-k8s-manifest chart at a given version from repo.f5.com
and print the bill of materials it pins (FLO, cert-gen, CWC, images).
Only the FAR key is needed — no cluster required, so this runs before
"cluster up" and answers "is this tag real?" without deploying anything.

With no argument it probes the version this binary is pinned to
(` + version.CNEManifestVersion + `). Pass a candidate tag to check a
release before bumping internal/version/version.go:

  ocibnkctl manifest probe 2.3.2-3.2598.3-0.0.392

probe verifies that the BOM's own releases[0].version matches the tag it
was pulled under, and fails if they diverge. That check exists because
F5's FLO upgrade doc and the real chart tag have silently disagreed
before (the 2.3.1 doc showed ...0.0.302; the chart was ...0.0.304).

Credentials come from the PoC's bnk.far_key_ref (default keys/f5-far-auth-key.tgz),
or from --far for a probe outside a PoC dir. Nothing is written to the
PoC unless --out is given; the chart is pulled into a temp dir and removed.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mv := version.CNEManifestVersion
			if len(args) == 1 {
				mv = args[0]
			}
			return runManifestProbe(cmd.Context(), cmd.OutOrStdout(), mv, f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC directory (default: current dir)")
	cmd.Flags().StringVar(&f.farPath, "far", "", "FAR auth tgz (overrides the PoC's bnk.far_key_ref)")
	cmd.Flags().StringVar(&f.out, "out", "", "Keep the pulled chart + manifest.yaml in this dir")
	cmd.Flags().BoolVar(&f.all, "all", false, "Print every chart and image, not just the ones ocibnkctl consumes")
	return cmd
}

// resolveFARPath decides which FAR tgz to authenticate with. An explicit
// --far wins outright (and needs no PoC at all); otherwise the PoC's
// bnk.far_key_ref is resolved relative to the PoC dir.
func resolveFARPath(pocDir, farFlag string) (string, error) {
	if farFlag != "" {
		return filepath.Abs(expandTilde(farFlag))
	}
	repo, err := resolvePoCDir(pocDir)
	if err != nil {
		return "", err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return "", fmt.Errorf("not a PoC repo (%s) and no --far given: %w", repo, err)
	}
	if p.BNK.FARKeyRef == "" {
		return "", fmt.Errorf("poc.yaml has no bnk.far_key_ref; pass --far <tgz>")
	}
	return resolveRef(repo, p.BNK.FARKeyRef), nil
}

func runManifestProbe(ctx context.Context, out io.Writer, manifestVersion string, f *manifestProbeFlags) error {
	farPath, err := resolveFARPath(f.pocDir, f.farPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(farPath); err != nil {
		return fmt.Errorf("FAR tgz not found at %s — drop the file there or pass --far", farPath)
	}

	// Pull into a scratch dir so a probe never disturbs the deploy-time
	// cache under artifacts/release-manifest (that one is the audit trail
	// for what was actually deployed; a speculative probe is not).
	cacheDir := f.out
	if cacheDir == "" {
		tmp, err := os.MkdirTemp("", "ocibnkctl-manifest-probe-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		cacheDir = tmp
	}
	helmHome := filepath.Join(cacheDir, "helm-home")

	fmt.Fprintf(out, "Probing %s/%s\n", version.ReleaseManifestRepo, version.ReleaseManifestChart)
	fmt.Fprintf(out, "  version:  %s\n", manifestVersion)
	fmt.Fprintf(out, "  FAR key:  %s\n\n", farPath)

	auth, err := deploy.ExtractFARRegistryAuth(farPath)
	if err != nil {
		return fmt.Errorf("extract FAR registry creds: %w", err)
	}
	m, err := deploy.PullReleaseManifest(ctx, auth, manifestVersion, cacheDir, helmHome)
	if err != nil {
		return fmt.Errorf("pull release-manifest %s: %w", manifestVersion, err)
	}

	m.SinkSummary(out)
	if f.all {
		printFullBOM(out, m)
	}
	if f.out != "" {
		fmt.Fprintf(out, "\n  kept in: %s\n", cacheDir)
	}

	// The tag/BOM agreement check — the reason this command exists.
	if m.Version != manifestVersion {
		fmt.Fprintln(out)
		return fmt.Errorf("release-manifest MISMATCH: pulled under tag %q but its releases[0].version is %q — "+
			"do not pin this tag until F5 resolves the discrepancy", manifestVersion, m.Version)
	}
	fmt.Fprintf(out, "\nOK: BOM releases[0].version matches the pulled tag.\n")

	if manifestVersion != version.CNEManifestVersion {
		fmt.Fprintf(out, "NOTE: this binary is pinned to %s — bump internal/version/version.go\n",
			version.CNEManifestVersion)
		fmt.Fprintf(out, "      (CNEManifestVersion) and the Makefile's BNK to adopt %s.\n", manifestVersion)
	}
	return nil
}

// printFullBOM dumps every chart and image in the manifest, sorted, so two
// probes can be diffed to see exactly what a release bump moves.
func printFullBOM(out io.Writer, m *deploy.ReleaseManifest) {
	for _, sec := range []struct {
		title string
		items map[string]string
	}{
		{"helm charts", m.HelmCharts},
		{"docker images", m.DockerImgs},
	} {
		names := make([]string, 0, len(sec.items))
		for n := range sec.items {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintf(out, "\n  %s (%d):\n", sec.title, len(names))
		for _, n := range names {
			fmt.Fprintf(out, "    %-45s %s\n", n, sec.items[n])
		}
	}
}
