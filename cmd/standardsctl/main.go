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
	fmt.Println("  flavors            Plan or sync moving version flavor tags (bleeding, latest, lts)")
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
	fmt.Println("  bump               Proactive prerelease bump train and ephemeral canary testing")
	fmt.Println("  paperclip          Paperclip agent harness synthesis and Rule 0 terminal disposition")
	fmt.Println("  changelog          Manage Keep-a-Changelog fragments and release sections")
	fmt.Println("  release            Execute release verification gates and render changelog")
	fmt.Println("  needs              Declare and report repository capabilities and demand to Golusoris")
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
	switch cmd {
	case "init":
		return runInit(args)
	case "compile-context":
		return runCompileContext(args)
	case "audit":
		return runAudit(args)
	case "baseline":
		return runBaseline(args)
	case "devcontainer":
		return runDevContainer(args)
	case "flavors":
		return runFlavors(args)
	case "models":
		return runModels(args)
	case "plan":
		return runPlan(args)
	case "sync":
		return runSync(args)
	case "sentinel":
		return runSentinel(args)
	case "worktree":
		return runWorktree(args)
	case "gc":
		return runGC(args)
	case "editors":
		return runEditors(args)
	case "forge":
		return runForge(args)
	case "harvest":
		return runHarvest(args)
	case "adopt", "conform", "bootstrap":
		return runAdopt(args)
	case "bump":
		return runBump(args)
	case "paperclip":
		return runPaperclip(args)
	case "changelog":
		return runChangelog(args)
	case "release":
		return runRelease(args)
	case "needs":
		return runNeeds(args)
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

