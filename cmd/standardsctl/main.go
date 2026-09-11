package main

import (
	"fmt"
	"os"
)

const version = "v1.0.0"

func printUsage() {
	fmt.Println("praetorctl (formerly standardsctl) - Autonomous Fleet Governance & Workstation Sentinel (" + version + ")")
	fmt.Println("\nUsage:")
	fmt.Println("  praetorctl <command> [arguments]  (alias: standardsctl)")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  init               Scaffold configuration, baseline, and agent context for new repo")
	fmt.Println("  compile-context    Transpile canonical AGENTS.md to vendor-native formats (< 300 LOC)")
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
	fmt.Println("  sentinel           Inspect workstation RAM/disk health and model headroom")
	fmt.Println("  worktree           Manage isolated ephemeral git worktrees")
	fmt.Println("  gc                 Garbage collect stale worktrees, caches, and logs")
	fmt.Println("  editors            Synthesize or verify IDE configurations (VSCode, Cursor, JetBrains, Neovim)")
	fmt.Println("  forge              Synchronize git provider wiki, issues, or validate PRs")
	fmt.Println("  harvest            Audit fleet repositories, workstation worktrees, and agent skills")
	fmt.Println("  adopt              Adopt and bootstrap any repository to 100% template compliance (alias: conform, bootstrap)")
	fmt.Println("  dogfood            Execute self-governance verification, local adoption simulations, and public repo benchmarks")
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

func dispatchCommand(cmd string, args []string) error {
	if err, ok := dispatchCoreCommand(cmd, args); ok {
		return err
	}
	return dispatchOperationsCommand(cmd, args)
}

func dispatchCoreCommand(cmd string, args []string) (error, bool) {
	switch cmd {
	case "init":
		return runInit(args), true
	case "compile-context":
		return runCompileContext(args), true
	case "audit":
		return runAudit(args), true
	case "baseline":
		return runBaseline(args), true
	case "devcontainer":
		return runDevContainer(args), true
	case "flavor":
		return runFlavor(args), true
	case "flavors":
		return runFlavors(args), true
	case "docs":
		return runDocs(args), true
	case "hindsight":
		return runHindsight(args), true
	case "state":
		return runState(args), true
	case "dedupe":
		return runDedupe(args), true
	case "models":
		return runModels(args), true
	case "plan":
		return runPlan(args), true
	case "sync":
		return runSync(args), true
	case "sentinel":
		return runSentinel(args), true
	case "worktree":
		return runWorktree(args), true
	case "gc":
		return runGC(args), true
	case "editors":
		return runEditors(args), true
	default:
		return nil, false
	}
}

func dispatchOperationsCommand(cmd string, args []string) error {
	switch cmd {
	case "forge":
		return runForge(args)
	case "harvest":
		return runHarvest(args)
	case "adopt", "conform", "bootstrap":
		return runAdopt(args)
	case "dogfood":
		return runDogfood(args)
	case "bump":
		return runBump(args)
	case "paperclip":
		return runPaperclip(args)
	case "changelog":
		return runChangelog(args)
	case "release":
		return runRelease(args)
	case "gate":
		return runGate(args)
	case "agent":
		return runAgent(args)
	case "serve":
		return runServe(args)
	case "sbom":
		return runSBOM(args)
	case "provenance":
		return runProvenance(args)
	case "needs":
		return runNeeds(args)
	case "issue":
		return runIssue(args)
	case "milestone":
		return runMilestone(args)
	case "project":
		return runProject(args)
	case "build":
		return runBuild(args)
	case "ci":
		return runCI(args)
	case "topology":
		return runTopology(args)
	case "version":
		fmt.Printf("standardsctl version %s\n", version)
		return nil
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("unknown command: %s", cmd)
	}
}
