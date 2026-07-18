package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/cluster"
	"github.com/mwiget/ocibnkctl/internal/deploy"
	"github.com/mwiget/ocibnkctl/internal/poc"
	"github.com/mwiget/ocibnkctl/internal/scenarios"

	// Side-effect imports: each blank-imported package registers its
	// scenario(s) with internal/scenarios at init time. Add new ones
	// here as they land.
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/aisemcache"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/aitokencount"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/aitokencountdssm"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/bgpanycast"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/bgppeer"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/clusterwidewatch"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/corefiles"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/cwcadminaccess"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/extrespool"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/ficdynamicip"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/grpcroute"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/httproutee2e"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/proxyprotocol"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/tcpl4lb"
	_ "github.com/mwiget/ocibnkctl/internal/scenarios/udpl4lb"
)

func newScenarioCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Run F5 BNK how-to scenarios against the running cluster",
		Long: `Each scenario maps to one F5 how-to article from
clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/ and
exercises a slice of BNK functionality end-to-end: render manifests
under artifacts/scenarios/<name>/, apply them, assert the reconciled
state, write a report under reports/<timestamp>/scenarios/<name>.json.

Rating tells you whether the scenario can actually run in the
ocibnkctl 2-node / demo-mode TMM shape:

  green   fully testable here
  amber   partially testable (some assertions skipped or relaxed)
  red     not testable; listed for discoverability, never executed`,
	}
	cmd.AddCommand(
		newScenarioListCmd(),
		newScenarioRunCmd(),
		newScenarioCleanCmd(),
	)
	return cmd
}

func newScenarioListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all known scenarios + their rating",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			items := scenarios.All()
			sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
			fmt.Fprintf(out, "%-22s %-7s %-22s %s\n", "NAME", "RATING", "DEPENDS-ON", "TITLE")
			for _, s := range items {
				deps := strings.Join(s.Dependencies(), ",")
				if deps == "" {
					deps = "-"
				}
				fmt.Fprintf(out, "%-22s %-7s %-22s %s\n", s.Name(), s.Rating(), deps, s.Title())
			}
			if len(items) == 0 {
				fmt.Fprintln(out, "(no scenarios registered)")
			}
			return nil
		},
	}
}

type scenarioRunFlags struct {
	pocDir  string
	all     bool
	dryRun  bool
	verbose bool
}

func newScenarioRunCmd() *cobra.Command {
	f := &scenarioRunFlags{}
	cmd := &cobra.Command{
		Use:   "run [name]",
		Short: "Run one scenario (or --all green-rated) against the cluster",
		Long: `Run a single scenario by name, or use --all to run every
green-rated scenario in dependency order (so dependencies run before
their dependents). Red-rated scenarios are always skipped, even with
--all. A single-name run does NOT auto-chain — surface that the
dependency isn't up rather than implicitly fixing it.

Manifests are rendered into artifacts/scenarios/<name>/ before any
cluster I/O. With --dry-run the rendered files are written but
nothing is applied — handy to inspect what would land.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScenarios(cmd.Context(), cmd.OutOrStdout(), args, f)
		},
	}
	cmd.Flags().StringVar(&f.pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&f.all, "all", false, "Run every green-rated scenario in registration order")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Render manifests but apply nothing")
	cmd.Flags().BoolVar(&f.verbose, "verbose", false, "Surface per-assertion lines + Details to stdout (always in the JSON report)")
	return cmd
}

func runScenarios(ctx context.Context, out io.Writer, args []string, f *scenarioRunFlags) error {
	if !f.all && len(args) != 1 {
		return fmt.Errorf("provide a scenario name OR --all (see `ocibnkctl scenario list`)")
	}
	if f.all && len(args) > 0 {
		return fmt.Errorf("--all and a positional name are mutually exclusive")
	}

	repo, err := resolvePoCDir(f.pocDir)
	if err != nil {
		return err
	}
	p, err := poc.Load(repo)
	if err != nil {
		return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
	}
	kubeconfig, err := requireKubeconfig(repo, "run `ocibnkctl cluster up` first")
	if err != nil {
		return err
	}
	// In-container preflight: a fresh container running against a resumed
	// workspace (e.g. a bnk-forge pipeline action) has not been attached to
	// the cluster's docker network by any deploy phase — attach or kubectl
	// hangs against the server-container IP (#22). No-op on the host.
	if err := cluster.EnsureReachable(ctx, p.Cluster.Provider, p.Cluster.Name); err != nil {
		return fmt.Errorf("make cluster network reachable: %w", err)
	}

	sctx := &scenarios.Context{
		Ctx:    ctx,
		PoC:    p,
		PoCDir: repo,
		Runner: &deploy.Runner{
			KubeconfigPath: kubeconfig,
			HelmHome:       repo + "/artifacts/helm-home",
			Out:            prefixWriter{w: out, prefix: "      | "},
		},
		Out:     out,
		DryRun:  f.dryRun,
		Verbose: f.verbose,
	}

	var todo []scenarios.Scenario
	if f.all {
		var greens []scenarios.Scenario
		for _, s := range scenarios.All() {
			if s.Rating() == scenarios.Green {
				greens = append(greens, s)
			}
		}
		if len(greens) == 0 {
			fmt.Fprintln(out, "no green-rated scenarios registered")
			return nil
		}
		ordered, err := topoSortByDeps(greens)
		if err != nil {
			return err
		}
		todo = ordered
		// One shared stamp so every per-scenario JSON + the run.json
		// aggregate land in the same reports/<stamp>/ dir.
		sctx.ReportStamp = time.Now().UTC().Format("2006-01-02T15-04-05Z")
	} else {
		s := scenarios.Find(args[0])
		if s == nil {
			return fmt.Errorf("unknown scenario %q (see `ocibnkctl scenario list`)", args[0])
		}
		todo = append(todo, s)
	}

	runStart := time.Now().UTC()
	failed := 0
	skipped := 0
	var entries []scenarios.SummaryEntry
	for _, s := range todo {
		scnStart := time.Now()
		r := scenarios.Run(sctx, s)
		dur := time.Since(scnStart).Truncate(time.Second).String()
		switch r.Status {
		case "failed":
			failed++
		case "skipped":
			skipped++
		}
		entries = append(entries, scenarios.SummaryEntry{
			Name:     s.Name(),
			Rating:   string(s.Rating()),
			Status:   r.Status,
			Duration: dur,
			Summary:  r.Summary,
		})
		fmt.Fprintln(out)
	}

	if f.all && sctx.ReportStamp != "" {
		finished := time.Now().UTC()
		sum := scenarios.RunSummary{
			StartedAt: runStart,
			Finished:  finished,
			Duration:  finished.Sub(runStart).Truncate(time.Second).String(),
			Total:     len(entries),
			Passed:    len(entries) - failed - skipped,
			Failed:    failed,
			Skipped:   skipped,
			Scenarios: entries,
		}
		base, err := scenarios.WriteRunSummary(repo, p.Metadata.Name, sctx.ReportStamp, sum)
		if err != nil {
			fmt.Fprintf(out, "warning: writing run summary: %v\n", err)
		} else {
			fmt.Fprintf(out, "summary: reports/%s/%s.{json,md}  —  %d passed, %d failed, %d skipped (%s)\n",
				sctx.ReportStamp, base, sum.Passed, sum.Failed, sum.Skipped, sum.Duration)
		}
	}

	if failed > 0 {
		return fmt.Errorf("%d scenario(s) failed", failed)
	}
	return nil
}

// topoSortByDeps returns ss in an order such that every scenario's
// declared Dependencies() come before it. Dependencies on scenarios
// outside ss (e.g. amber scenarios filtered out by --all) are
// ignored — the operator runs those separately.
func topoSortByDeps(ss []scenarios.Scenario) ([]scenarios.Scenario, error) {
	byName := map[string]scenarios.Scenario{}
	for _, s := range ss {
		byName[s.Name()] = s
	}
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := map[string]int{}
	var out []scenarios.Scenario
	var visit func(s scenarios.Scenario, stack []string) error
	visit = func(s scenarios.Scenario, stack []string) error {
		switch state[s.Name()] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle among scenarios: %s -> %s",
				strings.Join(stack, " -> "), s.Name())
		}
		state[s.Name()] = visiting
		for _, dep := range s.Dependencies() {
			d, ok := byName[dep]
			if !ok {
				continue
			}
			if err := visit(d, append(stack, s.Name())); err != nil {
				return err
			}
		}
		state[s.Name()] = visited
		out = append(out, s)
		return nil
	}
	for _, s := range ss {
		if err := visit(s, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func newScenarioCleanCmd() *cobra.Command {
	var pocDir string
	var all bool
	cmd := &cobra.Command{
		Use:   "clean [name]",
		Short: "Delete the cluster objects a scenario applied (or --all green-rated)",
		Long: `Delete the cluster objects a scenario applied. With --all, cleans
every green-rated scenario in REVERSE dependency order (dependents before
their dependencies, mirroring destroy), best-effort — one scenario's cleanup
failure doesn't abort the rest.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all && len(args) != 1 {
				return fmt.Errorf("provide a scenario name OR --all (see `ocibnkctl scenario list`)")
			}
			if all && len(args) > 0 {
				return fmt.Errorf("--all and a positional name are mutually exclusive")
			}
			repo, err := resolvePoCDir(pocDir)
			if err != nil {
				return err
			}
			p, err := poc.Load(repo)
			if err != nil {
				return fmt.Errorf("not a PoC repo (%s): %w", repo, err)
			}
			kubeconfig, err := requireKubeconfig(repo, "run `ocibnkctl cluster up` first")
			if err != nil {
				return err
			}
			// In-container preflight — see scenario run (#22).
			if err := cluster.EnsureReachable(cmd.Context(), p.Cluster.Provider, p.Cluster.Name); err != nil {
				return fmt.Errorf("make cluster network reachable: %w", err)
			}
			sctx := &scenarios.Context{
				Ctx:    cmd.Context(),
				PoC:    p,
				PoCDir: repo,
				Runner: &deploy.Runner{
					KubeconfigPath: kubeconfig,
					HelmHome:       repo + "/artifacts/helm-home",
					Out:            prefixWriter{w: cmd.OutOrStdout(), prefix: "      | "},
				},
				Out: cmd.OutOrStdout(),
			}

			var todo []scenarios.Scenario
			if all {
				var greens []scenarios.Scenario
				for _, s := range scenarios.All() {
					if s.Rating() == scenarios.Green {
						greens = append(greens, s)
					}
				}
				if len(greens) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no green-rated scenarios registered")
					return nil
				}
				ordered, err := topoSortByDeps(greens)
				if err != nil {
					return err
				}
				// Reverse the dependency order so dependents are removed before
				// the resources they lean on (BGP peering last).
				for i := len(ordered) - 1; i >= 0; i-- {
					todo = append(todo, ordered[i])
				}
			} else {
				s := scenarios.Find(args[0])
				if s == nil {
					return fmt.Errorf("unknown scenario %q (see `ocibnkctl scenario list`)", args[0])
				}
				todo = append(todo, s)
			}

			failed := 0
			for _, s := range todo {
				if err := scenarios.Cleanup(sctx, s); err != nil {
					// Best-effort: report and keep going so one failure can't
					// strand the rest of a bulk clean.
					fmt.Fprintf(cmd.OutOrStdout(), "warning: clean %s: %v\n", s.Name(), err)
					failed++
				}
			}
			if failed > 0 {
				return fmt.Errorf("%d scenario(s) failed to clean", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().BoolVar(&all, "all", false, "Clean every green-rated scenario in reverse dependency order")
	return cmd
}

