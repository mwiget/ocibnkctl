package cli

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/mwiget/ocibnkctl/internal/poc"
)

// Supported agentic CLIs. ocibnkctl does not embed an LLM; it prints the
// invocation the operator runs with their preferred CLI, each pointed at
// the PoC repo's AGENTS.md (which CLAUDE.md @-includes for Claude Code).
// The operator brings their own model + endpoint.
var agentRecipes = map[string]func(repo, endpoint string) string{
	"claude": func(repo, endpoint string) string {
		extra := ""
		if endpoint != "" {
			extra = "\n  ANTHROPIC_BASE_URL=" + endpoint + " \\"
		}
		return fmt.Sprintf(`# Claude Code (https://docs.claude.com/en/docs/claude-code)
cd %s && \%s
  claude
# Then say:
#   "Read AGENTS.md, then walk me through deploying BNK on this PoC
#    (validate -> cluster up -> deploy), explaining each phase as you go."
`, repo, extra)
	},
	"gemini": func(repo, endpoint string) string {
		return fmt.Sprintf(`# Gemini CLI
cd %s && \
  gemini chat --system-instruction "$(cat AGENTS.md)"
# Then ask it to validate the PoC and plan the deploy.
`, repo)
	},
	"aider": func(repo, endpoint string) string {
		base := ""
		if endpoint != "" {
			base = " --openai-api-base " + endpoint
		}
		return fmt.Sprintf(`# Aider
cd %s && \
  aider --read AGENTS.md%s poc.yaml
`, repo, base)
	},
	"openai": func(repo, endpoint string) string {
		base := endpoint
		if base == "" {
			base = "https://api.openai.com/v1  # or your local vLLM endpoint"
		}
		return fmt.Sprintf(`# Generic OpenAI-compatible REPL (e.g., llm, chatgpt-cli)
cd %s
export OPENAI_API_BASE=%s
# Load AGENTS.md as the system prompt. Example with simonw/llm:
#   llm --system "$(cat AGENTS.md)" "Read poc.yaml; confirm scope; plan the deploy."
`, repo, base)
	},
	"pi": func(repo, endpoint string) string {
		// pi auto-discovers and concatenates AGENTS.md (and CLAUDE.md)
		// from cwd, parent dirs, and ~/.pi/agent/ — so `cd` + `pi` is
		// enough. Install: curl -fsSL https://pi.dev/install.sh | sh
		return fmt.Sprintf(`# pi coding agent (https://pi.dev/)
cd %s && \
  pi
# pi auto-loads AGENTS.md from this directory (and parent dirs).
`, repo)
	},
	"opencode": func(repo, endpoint string) string {
		// opencode (Go-based TUI, https://opencode.ai/) treats AGENTS.md
		// as its project-config file by convention — bare invocation in
		// the directory is enough. NOTE: Anthropic blocked opencode from
		// Claude models in Jan 2026; pick a non-Anthropic provider.
		return fmt.Sprintf(`# OpenCode (https://opencode.ai/)
cd %s && \
  opencode
# AGENTS.md in this dir is opencode's project-config convention — auto-loaded.
# Pick a model with --model (Claude models are blocked for opencode since 2026-01).
`, repo)
	},
}

func newAgentCmd() *cobra.Command {
	var pocDir string
	cmd := &cobra.Command{
		Use:   "agent [claude|gemini|aider|openai|pi|opencode]",
		Short: "Print invocation for an agentic CLI driving this PoC repo",
		Long: `Print the command-line invocation for your preferred agentic CLI,
configured to load this PoC's AGENTS.md (the operator+agent guide that
CLAUDE.md @-includes for Claude Code).

ocibnkctl does not embed an LLM. You choose which CLI to use and where
its API endpoint is (cloud vendor, local vLLM, etc.) — set --llm-endpoint
or the CLI's own env var (ANTHROPIC_BASE_URL / OPENAI_API_BASE).

Without an argument, lists all supported CLIs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			endpoint, _ := cmd.Flags().GetString("llm-endpoint")

			// Resolve the PoC dir to `cd` into. The bare listing form is
			// global info — don't fail just because cwd isn't a PoC repo.
			repo, repoErr := resolvePoCDir(pocDir)
			var p *poc.PoC
			if repoErr == nil {
				if loaded, err := poc.Load(repo); err == nil {
					p = loaded
				}
			}

			if len(args) == 0 {
				fmt.Fprintln(out, "Supported agentic CLIs:")
				names := make([]string, 0, len(agentRecipes))
				for name := range agentRecipes {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					fmt.Fprintf(out, "  - %s\n", name)
				}
				fmt.Fprintf(out, "\nRun:  %s agent <name>  to print its invocation.\n", invocationName())
				return nil
			}

			// Printing an invocation just needs a directory to `cd` into.
			// The recipes work against any dir with an AGENTS.md (e.g. a
			// PoC, or the ocibnkctl source tree itself).
			if repoErr != nil {
				return repoErr
			}
			recipe, ok := agentRecipes[args[0]]
			if !ok {
				return fmt.Errorf("unknown agent %q (try: claude, gemini, aider, openai, pi, opencode)", args[0])
			}
			fmt.Fprint(out, recipe(repo, endpoint))
			if p == nil {
				fmt.Fprintf(out, "\n# NOTE: %s has no poc.yaml — recipe was templated for this directory anyway.\n", repo)
				fmt.Fprintf(out, "# To target a PoC, pass --poc <dir> or cd into one created by `%s init`.\n", invocationName())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&pocDir, "poc", "", "PoC repo path (default: current directory)")
	cmd.Flags().String("llm-endpoint", "", "OpenAI-compatible / Anthropic-compatible base URL")
	return cmd
}
