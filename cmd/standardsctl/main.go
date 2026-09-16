package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

// version is written at release time with -X main.version. It must stay a var: the Go linker
// cannot write a const, so every -X injection was silently discarded and every build ever
// produced -- releases included -- reported the same literal (#119). A build that carries no
// injected version says what it can prove instead of naming one it cannot.
var version = ""

// buildVersion reports the version this binary can actually prove it is.
//
// A release carries an injected version. Any other build carries the revision Go records in
// its build information, which identifies the tree exactly. When neither is present the answer
// is "unknown", never a plausible-looking constant: .standards.lock records this string as
// pinned_version, and a lock naming a version nothing measured cannot say which praetor
// governed a repository, which is the whole point of writing it down.
func buildVersion() string {
	if trimmed := strings.TrimSpace(version); trimmed != "" {
		return trimmed
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown (no build information)"
	}
	revision, modified := vcsStamp(info)
	if revision == "" {
		return "unknown (untagged build, no VCS stamp)"
	}
	if modified {
		return revision + "-dirty"
	}
	return revision
}

// lockVersion is the string init records as .standards.lock pinned_version.
//
// The lock is how a repository says which praetor governed it, and the field is validated as
// SemVer. A release writes its own version. Any other build writes v0.0.0 with the revision as
// build metadata -- still valid SemVer, and it names the exact tree, which "v1.0.0" never did.
// A build that can identify nothing returns ok=false so the caller can say so rather than
// writing a version it cannot stand behind.
func lockVersion() (string, bool) {
	if trimmed := strings.TrimSpace(version); trimmed != "" {
		return trimmed, true
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unidentifiedLockVersion, false
	}
	revision, modified := vcsStamp(info)
	if revision == "" {
		return unidentifiedLockVersion, false
	}
	return formatDevLockVersion(revision, modified), true
}

// formatDevLockVersion renders an unreleased build's pinned_version.
//
// The revision goes in SemVer build metadata, where a dot separates identifiers and a second
// plus sign would be invalid. Getting that wrong produces a lock the validator rejects, so the
// shape is pinned by a test rather than left to inspection.
func formatDevLockVersion(revision string, modified bool) string {
	if modified {
		return "v0.0.0+" + revision + ".dirty"
	}
	return "v0.0.0+" + revision
}

// unidentifiedLockVersion is written only when the build can prove nothing about itself. It is
// deliberately the zero version rather than a plausible release number.
const unidentifiedLockVersion = "v0.0.0"

// vcsStamp extracts the revision and dirty flag Go embeds at build time.
func vcsStamp(info *debug.BuildInfo) (revision string, modified bool) {
	for i := 0; i < len(info.Settings) && i < maxBuildSettings; i++ {
		switch info.Settings[i].Key {
		case "vcs.revision":
			revision = info.Settings[i].Value
			if len(revision) > shortRevisionLen {
				revision = revision[:shortRevisionLen]
			}
		case "vcs.modified":
			modified = info.Settings[i].Value == "true"
		}
	}
	return revision, modified
}

// maxBuildSettings bounds the build-information scan (HISS-02).
const maxBuildSettings = 256

// shortRevisionLen is how much of a commit hash identifies a build in output.
const shortRevisionLen = 12

// maxCSVFields bounds the comma-separated list parser (HISS-02).
const maxCSVFields = 1024

func printUsage() {
	fmt.Println("praetorctl (formerly standardsctl) - Autonomous Fleet Governance & Workstation Sentinel (" + buildVersion() + ")")
	fmt.Println("\nUsage:")
	fmt.Println("  praetorctl <command> [arguments]  (alias: standardsctl)")
	fmt.Println("\nAvailable Commands:")
	printCoreCommands()
	printFleetCommands()
	fmt.Println("\nRun 'standardsctl <command> -h' for more information on a command.")
}

// printCoreCommands lists the repository-scoped commands. The listing is split across two
// functions to stay inside the HISS-04 statement bound rather than suppressing it.
func printCoreCommands() {
	fmt.Println("  init               Scaffold configuration, baseline, and agent context for new repo")
	fmt.Println("  compile-context    Transpile canonical AGENTS.md to vendor-native formats (< 300 LOC)")
	fmt.Println("  context-optimize   Analyze explicit context files and optionally write a private review pack")
	fmt.Println("  clients            Prepare or apply client configurations from one shared tool registry")
	fmt.Print("  notebook           Prepare source-grounded planning templates or validate generated drafts\n" +
		"  planning           Compile a structural planning draft and optionally write private artifacts\n")
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
	fmt.Println("  hiss               Inspect and verify declared HISS enforcement evidence")
	fmt.Println("  models             Sync or list active model tiers and benchmark limits")
	fmt.Println("  plan               Dry-run comparison of repository settings against policy")
	fmt.Println("  sync               Reconcile repository settings, labels, and branch rulesets")
	fmt.Println("  operational        Plan or prepare an operational fork from reviewed local commits")
	fmt.Println("  sentinel           Inspect workstation RAM/disk health and model headroom")
	fmt.Println("  worktree           Manage isolated ephemeral git worktrees")
}

// printFleetCommands lists the fleet, forge and delivery commands.
func printFleetCommands() {
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
	fmt.Println("  wishes             Read and apply private repository wishes and polls")
	fmt.Println("  milestone          Manage local and remote GitHub milestones and progress")
	fmt.Println("  project            Manage GitHub Projects v2 boards and track epic issues")
	fmt.Println("  build              Request a polyglot build (execution backends currently unavailable)")
	fmt.Println("  ci                 Analyze git diff and filter CI verification gates")
	fmt.Println("  topology           Audit and clean workstation directory topology (DEV-01 to DEV-05)")
	fmt.Println("  version            Print CLI version information")
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
		"planning":         runPlanning,
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
		"hiss":             runHiss,
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
		"wishes":           runWishes,
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
	fmt.Printf("praetorctl version %s\n", buildVersion())
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
