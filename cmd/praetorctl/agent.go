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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	default:
		return fmt.Errorf("unknown agent persona: %s (run 'praetorctl agent list' to view available agents)", agentName)
	}
}

func runAuditorAgent(ctx context.Context) error {
	opts := hiss.ScanOptions{Cap: 1000, MaxFuncLOC: 60}
	rep, err := hiss.Scan(ctx, ".", opts)
	if err != nil {
		return fmt.Errorf("auditor execution error: %w", err)
	}
	fmt.Printf("[praetor-auditor] AST sweep complete: %d infractions detected\n", rep.TotalInfractions)
	return nil
}

func runGatekeeperAgent(ctx context.Context) error {
	rep, err := gating.RunGatedPipeline(ctx, ".", false)
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
