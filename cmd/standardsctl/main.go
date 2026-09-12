package main

import (
	"fmt"
	"os"
	"strings"
)

const version = "v1.0.0"

// maxCSVFields bounds the comma-separated list parser (HISS-02).
const maxCSVFields = 1024

func printUsage() {
	fmt.Println("praetorctl (formerly standardsctl) - Autonomous Fleet Governance & Workstation Sentinel (" + version + ")")
	fmt.Println("\nUsage:")
	fmt.Println("  praetorctl <command> [arguments]  (alias: standardsctl)")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  init               Scaffold configuration, baseline, and agent context for new repo")
	fmt.Println("  compile-context    Transpile canonical AGENTS.md to vendor-native formats (< 300 LOC)")
	fmt.Println("  context-optimize   Analyze explicit context files and optionally write a private review pack")
	fmt.Println("  clients            Prepare or apply client configurations from one shared tool registry")
	fmt.Println("  notebook           Prepare source-grounded planning templates or validate generated drafts")
	fmt.Println("  prompt-optimize    Select prompts from comparable held-out model/provider evaluations")
	fmt.Println("  audit              Audit repository against declared HISS-16 invariants and lockfile")
	fmt.Println("  baseline           Inspect or record technical debt infractions")
	fmt.Println("  devcontainer       Synthesize or verify .devcontainer/devcontainer.json")
	fmt.Println("  flavor             Inspect, audit, and scaffold engineering flavors (11 archetypes)")
	fmt.Println("  flavors            Plan or sync moving version flavor tags (bleeding, latest, lts)")
	fmt.Println("  docs               Harvest, compress, and audit package documentation sheets")
	fmt.Println("  hindsight          Manage local zero-token memory cache and sync with Hindsight server")
	fmt.Println("  state              Manage .workingdir/ session state, bugs ledger, and questions")
	fmt.Println("  dedupe             Scan for AST clones, utility sprawl, and cadence enforcement")
	fmt.Println("  models             Sync or list active model tiers and benchmark limits")
	fmt.Println("  plan               Dry-run comparison of repository settings against policy")
	fmt.Println("  sync               Reconcile repository settings, labels, and branch rulesets")
	fmt.Println("  operational        Plan or prepare an operational fork from reviewed local commits")
	fmt.Println("  sentinel           Inspect workstation RAM/disk health and model headroom")
	fmt.Println("  worktree           Manage isolated ephemeral git worktrees")
	fmt.Println("  gc                 Garbage collect stale worktrees, caches, and logs")
	fmt.Println("  editors            Synthesize or verify IDE configurations (VSCode, Cursor, JetBrains, Neovim)")
	fmt.Println("  forge              Synchronize git provider wiki, issues, or validate PRs")
	fmt.Println("  harvest            Audit fleet repositories, workstation worktrees, and agent skills")
	fmt.Println("  adopt              Adopt and bootstrap any repository to 100% template compliance (alias: conform, bootstrap)")
	fmt.Println("  dogfood            Run local dogfood suites, schedules, adoption benchmarks, and bounded repairs")
	fmt.Println("  bump               Proactive prerelease bump train and ephemeral canary testing")
	fmt.Println("  paperclip          Paperclip agent harness synthesis and Rule 0 terminal disposition")
	fmt.Println("  changelog          Manage Keep-a-Changelog fragments and release sections")
	fmt.Println("  release            Execute release verification gates and render changelog")
	fmt.Println("  gate               Execute 4-stage anti-direct-merge gating pipeline")
	fmt.Println("  agent              Manage and dispatch autonomous Praetor agent helpers (list, run)")
	fmt.Println("  serve              Run cloud-native container daemon with HTTP health probes")
	fmt.Println("  sbom               Generate CycloneDX 1.5 Software Bill of Materials")
	fmt.Println("  provenance         Generate SLSA v1.0 provenance attestation statement")
	fmt.Println("  needs              Declare and report repository capabilities and demand to Golusoris")
	fmt.Println("  issue              Reconcile cross-repo dependencies and unblock ready tasks")
	fmt.Println("  milestone          Manage local and remote GitHub milestones and progress")
	fmt.Println("  project            Manage GitHub Projects v2 boards and track epic issues")
	fmt.Println("  build              Compile polyglot targets with universal builder and pre-build optimizer")
	fmt.Println("  ci                 Analyze git diff and filter CI verification gates")
	fmt.Println("  topology           Audit and clean workstation directory topology (DEV-01 to DEV-05)")
	fmt.Println("  version            Print CLI version information")
	fmt.Println("\nRun 'standardsctl <command> -h' for more information on a command.")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	if err := dispatchCommand(cmd, args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// commandFunc is the signature every top-level command implements.
type commandFunc func(args []string) error

// commandTable maps every command name (and alias) to its handler. A table keeps
// dispatch at constant complexity regardless of how many commands exist (HISS-04).
func commandTable() map[string]commandFunc {
	return map[string]commandFunc{
		"init":             runInit,
		"compile-context":  runCompileContext,
		"context-optimize": runContextOptimize,
		"notebook":         runNotebook,
		"prompt-optimize":  runPromptOptimize,
		"audit":            runAudit,
		"baseline":         runBaseline,
		"devcontainer":     runDevContainer,
		"flavor":           runFlavor,
		"flavors":          runFlavors,
		"docs":             runDocs,
		"hindsight":        runHindsight,
		"state":            runState,
		"dedupe":           runDedupe,
		"models":           runModels,
		"operational":      runOperational,
		"plan":             runPlan,
		"sync":             runSync,
		"sentinel":         runSentinel,
		"worktree":         runWorktree,
		"gc":               runGC,
		"editors":          runEditors,
		"clients":          runClients,
		"forge":            runForge,
		"harvest":          runHarvest,
		"adopt":            runAdopt,
		"conform":          runAdopt,
		"bootstrap":        runAdopt,
		"dogfood":          runDogfood,
		"bump":             runBump,
		"paperclip":        runPaperclip,
		"changelog":        runChangelog,
		"release":          runRelease,
		"gate":             runGate,
		"agent":            runAgent,
		"serve":            runServe,
		"sbom":             runSBOM,
		"provenance":       runProvenance,
		"needs":            runNeeds,
		"issue":            runIssue,
		"milestone":        runMilestone,
		"project":          runProject,
		"build":            runBuild,
		"ci":               runCI,
		"topology":         runTopology,
		"version":          runVersion,
		"help":             runHelp,
		"-h":               runHelp,
		"--help":           runHelp,
	}
}

func dispatchCommand(cmd string, args []string) error {
	if handler, ok := commandTable()[cmd]; ok {
		return handler(args)
	}
	printUsage()
	return fmt.Errorf("unknown command: %s", cmd)
}

func runVersion(_ []string) error {
	fmt.Printf("standardsctl version %s\n", version)
	return nil
}

func runHelp(_ []string) error {
	printUsage()
	return nil
}

// splitCSV splits a comma-separated flag value into trimmed, non-empty fields.
func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && i < maxCSVFields; i++ {
		if trimmed := strings.TrimSpace(parts[i]); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return fields
}
