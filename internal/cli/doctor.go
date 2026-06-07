package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/version"
)

func newDoctorCmd() *cobra.Command {
	var strict bool
	var profile string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Verify host tooling (docker/podman, kubectl, helm) and report resources",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), strict, profile)
		},
	}
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail on warnings (e.g. min-resources not measured)")
	cmd.Flags().StringVar(&profile, "profile", "auto", "Resource profile: auto (small floor when host < 10 cores, else standard) | standard (10-core floor) | small (4-core/16GB Raspberry-Pi floor; needs deploy shrink + host_profile=small)")
	return cmd
}

type checkResult struct {
	name   string
	status string // ok | warn | fail
	detail string
}

func runDoctor(ctx context.Context, out io.Writer, strict bool, profile string) error {
	var results []checkResult

	results = append(results, checkRuntime(ctx))
	// No third-party cluster orchestrator to check — the k3s backend
	// runs nodes directly on the container runtime verified above.
	results = append(results, checkBinary(ctx, "kubectl", []string{"version", "--client=true", "--output=yaml"}))
	results = append(results, checkBinary(ctx, "helm", []string{"version", "--short"}))
	results = append(results, checkResources(profile))

	fails, warns := 0, 0
	for _, r := range results {
		badge := "✓"
		if r.status == "warn" {
			badge = "!"
			warns++
		}
		if r.status == "fail" {
			badge = "✗"
			fails++
		}
		fmt.Fprintf(out, "  [%s] %-12s  %s\n", badge, r.name, r.detail)
		if r.status == "fail" {
			if h := installHint(r.name); h != "" {
				fmt.Fprint(out, h)
			}
		}
	}
	fmt.Fprintln(out)
	if fails > 0 {
		return fmt.Errorf("%d failed check(s) — install the missing tools (commands above) and retry", fails)
	}
	if strict && warns > 0 {
		return fmt.Errorf("%d warning(s) with --strict", warns)
	}
	fmt.Fprintln(out, "ready.")
	return nil
}

// checkRuntime tries docker then podman.
func checkRuntime(ctx context.Context) checkResult {
	for _, rt := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(rt); err != nil {
			continue
		}
		c := exec.CommandContext(ctx, rt, "version")
		var stderr bytes.Buffer
		c.Stderr = &stderr
		c.Stdout = io.Discard
		if err := c.Run(); err != nil {
			return checkResult{rt, "fail",
				fmt.Sprintf("found on PATH but `%s version` failed: %v", rt, err)}
		}
		return checkResult{rt, "ok", "available"}
	}
	return checkResult{"runtime", "fail", "neither docker nor podman found on PATH"}
}

// installHint returns OS/arch-aware install guidance for a tool that
// doctor found missing, indented to sit under its check line. Returns ""
// when there's no canned hint (e.g. a tool present but broken). The
// commands are copy-pasteable; an agent driving a PoC can offer to run
// them (with the operator's OK — installing host tools is a system
// change).
func installHint(name string) string {
	goos, goarch := runtime.GOOS, runtime.GOARCH
	mac := goos == "darwin"
	switch name {
	case "runtime":
		if mac {
			return "       → brew install colima docker   (or install Docker Desktop)\n" +
				"         docs: https://docs.docker.com/desktop/\n"
		}
		return "       → curl -fsSL https://get.docker.com | sh   (docker)\n" +
			"         or podman: https://podman.io/docs/installation\n"
	case "kubectl":
		s := fmt.Sprintf("       → curl -LO \"https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/%s/%s/kubectl\" \\\n"+
			"           && chmod +x kubectl && sudo mv kubectl /usr/local/bin/\n", goos, goarch)
		if mac {
			s += "         or: brew install kubectl\n"
		}
		return s + "         docs: https://kubernetes.io/docs/tasks/tools/\n"
	case "helm":
		s := "       → curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash\n"
		if mac {
			s += "         or: brew install helm\n"
		}
		return s + "         docs: https://helm.sh/docs/intro/install/\n"
	}
	return ""
}

func checkBinary(ctx context.Context, name string, args []string) checkResult {
	if _, err := exec.LookPath(name); err != nil {
		return checkResult{name, "fail", "not on PATH"}
	}
	c := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return checkResult{name, "fail",
			fmt.Sprintf("`%s %s` failed: %s", name, strings.Join(args, " "),
				strings.TrimSpace(stderr.String()))}
	}
	first := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)[0]
	return checkResult{name, "ok", first}
}

// resolveProfile maps the --profile flag to a concrete profile. "auto" (the
// default) picks "small" when the host falls below the standard core floor
// but the small-host path can still carry it, otherwise "standard". An
// explicit profile is passed through untouched.
func resolveProfile(profile string, cores int) string {
	if profile != "auto" {
		return profile
	}
	if coresBelowFloor(cores) {
		return "small"
	}
	return "standard"
}

func checkResources(profile string) checkResult {
	cores := runtime.NumCPU()
	profile = resolveProfile(profile, cores)
	baseline := version.BaselineFor(profile)
	label := "min baseline"
	if profile == "small" {
		label = "small-host floor"
	}
	if !version.Measured() {
		return checkResult{
			"resources", "warn",
			fmt.Sprintf("host: %d cores | %s: not yet measured (TBD)", cores, label),
		}
	}
	if cores < baseline.Cores {
		return checkResult{"resources", "fail",
			fmt.Sprintf("host: %d cores  <  %s %d cores",
				cores, label, baseline.Cores)}
	}
	detail := fmt.Sprintf("host: %d cores  ≥  %s %d cores",
		cores, label, baseline.Cores)
	if profile == "small" {
		// On a tight host the chart only schedules once shrink + metrics-off
		// are in effect — both now happen without manual steps: `init` pins
		// bnk.host_profile=small in poc.yaml (TMM metrics off) and `e2e`
		// auto-runs deploy shrink. Note that, and how to opt out.
		detail += "\n" + strings.Repeat(" ", 20) +
			"note: tight host — init pins bnk.host_profile=small (TMM metrics off) & e2e auto-runs deploy shrink; set host_profile=standard to opt out"
	}
	return checkResult{"resources", "ok", detail}
}
