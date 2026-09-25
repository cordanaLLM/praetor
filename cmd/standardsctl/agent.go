package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/needs"
)

func runAgent(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		return listAgents()
	}

	if args[0] == "run" {
		if len(args) < 2 {
			return fmt.Errorf("usage: praetorctl agent run <agent-name>")
		}
		return dispatchAgentTask(args[1], args[2:])
	}

	return fmt.Errorf("unknown agent subcommand: %s (available: list, run)", args[0])
}

func listAgents() error {
	agentsDir := filepath.Join(".agents", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return fmt.Errorf("read agents directory: %w", err)
	}

	fmt.Printf("=== Available Praetor Autonomous Agent Helpers ===\n\n")
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			name := strings.TrimSuffix(e.Name(), ".md")
			fmt.Printf("  • %-25s (.agents/agents/%s)\n", name, e.Name())
		}
	}
	fmt.Printf("\nExecute an agent helper: praetorctl agent run <agent-name>\n")
	return nil
}

func dispatchAgentTask(agentName string, extraArgs []string) error {
	// extraArgs carried no meaning until now (BUG-726): the parameter was never read, so
	// a typo'd or unsupported flag after the agent name was silently dropped instead of
	// refused.
	if len(extraArgs) != 0 {
		return fmt.Errorf("agent run %s: unexpected extra argument(s) %v (agent helpers take no arguments)", agentName, extraArgs)
	}

	ctx, cancel := agentContext(agentName)
	defer cancel()

	fmt.Printf("[Agent Dispatch] Activating autonomous agent helper: %s\n", agentName)
	switch agentName {
	case "praetor-auditor", "praetor_auditor":
		return runAuditorAgent(ctx)
	case "praetor-gatekeeper", "praetor_gatekeeper":
		return runGatekeeperAgent(ctx)
	case "praetor-dogfooder", "praetor_dogfooder":
		return runDogfooderAgent(ctx)
	case "praetor-needs-miner", "praetor_needs_miner":
		return runNeedsMinerAgent(ctx)
	case "praetor-fuzzer", "praetor_fuzzer", "praetor-packager", "praetor_packager":
		// listAgents() enumerates every *.md file under .agents/agents, which includes
		// these two personas; dispatch has no Go implementation for either yet (BUG-726).
		// Naming them here refuses by name instead of falling into the generic "unknown
		// persona" branch below, which would wrongly conflate an advertised-but-
		// unimplemented persona with a genuine typo.
		return fmt.Errorf("agent persona %s is listed by 'agent list' but has no dispatchable implementation yet", agentName)
	default:
		return fmt.Errorf("unknown agent persona: %s (run 'praetorctl agent list' to view available agents)", agentName)
	}
}

// agentTimeout bounds every agent helper except the gatekeeper.
const agentTimeout = 5 * time.Minute

// agentContext bounds one agent helper. The gatekeeper runs the full gating pipeline, so it takes
// the gate run's own deadline: a fixed five minutes cut its race stage short (#314).
func agentContext(agentName string) (context.Context, context.CancelFunc) {
	if agentName == "praetor-gatekeeper" || agentName == "praetor_gatekeeper" {
		return gating.WithRunDeadline(context.Background(), gating.EnvRunBudget())
	}
	return context.WithTimeout(context.Background(), agentTimeout)
}

func runAuditorAgent(ctx context.Context) error {
	// Bounds come from the package defaults so this agent reports the same scope that audit
	// and the gate do; divergent literals made the three disagree (BUG-829).
	rep, err := hiss.Scan(ctx, ".", hiss.ScanOptions{})
	if err != nil {
		return fmt.Errorf("auditor execution error: %w", err)
	}
	if rep.Incomplete() {
		fmt.Printf("[praetor-auditor] AST sweep INCOMPLETE: %d infraction(s) detected so far; %s\n",
			rep.TotalInfractions, rep.CoverageEvidence())
		return fmt.Errorf("auditor scan did not cover its scope: %s", rep.CoverageEvidence())
	}
	fmt.Printf("[praetor-auditor] AST sweep complete: %d infractions detected\n", rep.TotalInfractions)
	return nil
}

func runGatekeeperAgent(ctx context.Context) error {
	rep, err := gatedPipeline(ctx, ".", false)
	if err != nil {
		return fmt.Errorf("gatekeeper execution error: %w", err)
	}
	fmt.Printf("[praetor-gatekeeper] Gated pipeline completed: %s\n", rep.Status)
	return nil
}

func runDogfooderAgent(ctx context.Context) error {
	// The zero value of ApplyAdoption keeps every adoption a simulation. VerifyOnly makes a
	// failed host audit an error rather than a line of prose the caller can ignore.
	opts := dogfood.DogfoodOptions{HostRepoPath: ".", VerifyOnly: true}
	rep, err := dogfood.RunDogfood(ctx, opts)
	if err != nil {
		return fmt.Errorf("dogfooder execution error: %w", err)
	}
	if !rep.OverallPassed {
		return fmt.Errorf("dogfooder verification failed on %s: context sync passed=%t, self audit passed=%t",
			rep.HostRepoPath, rep.ContextSyncPassed, rep.SelfAuditPassed)
	}
	fmt.Printf("[praetor-dogfooder] Adoption simulation verified on %s\n", rep.HostRepoPath)
	return nil
}

func runNeedsMinerAgent(ctx context.Context) error {
	rep, err := needs.ScanRepo(ctx, ".")
	if err != nil {
		return fmt.Errorf("needs miner error: %w", err)
	}
	fmt.Printf("[praetor-needs-miner] Scan complete: Readiness %.1f%% (%d covered, %d gaps)\n",
		rep.Readiness.Score, rep.Readiness.CoveredDeps, rep.Readiness.GapDeps)
	return nil
}
