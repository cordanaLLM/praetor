package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
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
	fmt.Println("  compile-framework-assets  Write a framework kit's llms.txt, agent rule and starter templates")
	fmt.Println("  context-optimize  Analyze explicit context files and optionally write a private review pack")
	fmt.Println("  caveman            Lint agent-facing text (check) or estimate its token cost (estimate)")
	fmt.Println("  clients            Prepare or apply client configurations from one shared tool registry")
	fmt.Print("  notebook           Prepare source-grounded planning templates or validate generated drafts\n" +
		"  planning           Compile a structural planning draft and optionally write private artifacts\n")
	fmt.Println("  prompt-optimize    Select prompts from comparable held-out model/provider evaluations")
	fmt.Println("  audit              Audit repository against declared HISS invariants and lockfile")
	fmt.Println("  bugs audit         Report bug ledger rows whose recorded location no longer resolves")
	fmt.Println("  baseline           Inspect or record technical debt infractions")
	fmt.Println("  devcontainer       Synthesize or verify .devcontainer/devcontainer.json")
	fmt.Println("  flavor             Inspect, audit, and scaffold engineering flavors (11 archetypes)")
	fmt.Println("  flavors            Plan or sync moving version flavor tags (bleeding, latest, lts)")
	fmt.Println("  docs               Harvest, compress, and audit package documentation sheets")
	fmt.Println("  hindsight          Manage local zero-token memory cache and sync with Hindsight server")
	fmt.Println("  state              Manage .workingdir/ session state, bugs ledger, and questions")
	fmt.Println("  dedupe             Scan for AST clones, utility sprawl, and cadence enforcement")
	fmt.Println("  hiss               Inspect and verify declared HISS enforcement evidence")
	fmt.Println("  hook               Serve one agent-hook event: praetorctl hook <client> <event>")
	fmt.Println("  models             Sync or list active model tiers and benchmark limits")
	fmt.Println("  plan               Dry-run comparison of repository settings against policy")
	fmt.Println("  sync               Reconcile repository settings, labels, and branch rulesets")
	fmt.Println("  operational        Plan or prepare an operational fork from reviewed local commits")
	fmt.Println("  adr verify         Replay the machine-checkable clauses of decision records")
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
	fmt.Println("  gate               Execute 6-stage anti-direct-merge gating pipeline")
	fmt.Println("  agent              Manage and dispatch autonomous Praetor agent helpers (list, run)")
	fmt.Println("  serve              Run cloud-native container daemon with HTTP health probes")
	fmt.Println("  sbom               Generate CycloneDX 1.5 Software Bill of Materials")
	fmt.Println("  provenance         Generate an unsigned SLSA v1.0 provenance statement for an artifact file")
	fmt.Println("  needs              Declare and report repository capabilities and demand to Golusoris")
	fmt.Println("  issue              Reconcile cross-repo dependencies and unblock ready tasks")
	fmt.Println("  wishes             Read and apply private repository wishes and polls")
	fmt.Println("  milestone          Manage local and remote GitHub milestones and progress")
	fmt.Println("  project            Manage GitHub Projects v2 boards and track epic issues")
	fmt.Println("  build              Request a polyglot build (execution backends currently unavailable)")
	fmt.Println("  ci                 Analyze git diff and filter CI verification gates")
	fmt.Println("  topology           Audit and clean workstation directory topology (DEV-01, DEV-02)")
	fmt.Println("  workstation        Install this engine's binaries from a checkout, or report install status")
	fmt.Println("  devsync            Copy project folders to Google Drive through rclone and back (stopgap)")
	fmt.Println("  version            Print CLI version information")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]
	if !ownsTerminationSignals(cmd) {
		// Every command praetorctl runs sits in a process group of its own, out of reach of
		// a terminal's Ctrl-C, and no subcommand observes the signal: this forwards the
		// signal to those groups, so git still cleans up, and kills any group still running
		// after a grace before the signal ends praetorctl, instead of leaving them running.
		util.TerminateCommandsOnSignal(os.Exit)
	}

	if err := dispatchCommand(cmd, args); err != nil {
		// flag.ErrHelp means a subcommand's own flag.Parse saw -h/--help and already
		// printed its usage; that is a satisfied request, not a failure (BUG-811). Only
		// two of the ~25 flag-parsed commands converted it to nil themselves, so every
		// other one reported "Error: flag: help requested" and exited 1 on --help.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(commandExitCode(os.Stderr, err))
	}
}

// ownsTerminationSignals reports whether command handles SIGINT and SIGTERM itself. serve
// drains its health server on them (container.WaitForGracefulDrain), which a handler that
// ends the process on the signal would cut short.
func ownsTerminationSignals(command string) bool {
	return command == "serve"
}

// commandFunc is the signature every top-level command implements.
type commandFunc func(args []string) error

// commandTable maps every command name (and alias) to its handler. A table keeps
// dispatch at constant complexity regardless of how many commands exist (HISS-04). It is
// split into two literals, merged here, purely to stay inside the HISS-04 LOC bound; the
// split carries no meaning dispatchCommand relies on.
func commandTable() map[string]commandFunc {
	table := make(map[string]commandFunc, len(coreCommandTable())+len(fleetCommandTable()))
	for name, handler := range coreCommandTable() {
		table[name] = handler
	}
	for name, handler := range fleetCommandTable() {
		table[name] = handler
	}
	return table
}

// coreCommandTable holds the repository-scoped commands (see printCoreCommands).
func coreCommandTable() map[string]commandFunc {
	return map[string]commandFunc{
		"init":                     runInit,
		"compile-context":          runCompileContext,
		"compile-framework-assets": runCompileFrameworkAssets,
		"context-optimize":         runContextOptimize,
		"caveman":                  runCaveman,
		"notebook":                 runNotebook,
		"planning":                 runPlanning,
		"prompt-optimize":          runPromptOptimize,
		"audit":                    runAudit,
		"adr":                      runADR,
		"bugs":                     runBugs,
		"baseline":                 runBaseline,
		"devcontainer":             runDevContainer,
		"devsync":                  runDevsync,
		"flavor":                   runFlavor,
		"flavors":                  runFlavors,
		"docs":                     runDocs,
		"hindsight":                runHindsight,
		"state":                    runState,
		"dedupe":                   runDedupe,
		"hiss":                     runHiss,
		"hook":                     runHook,
		"models":                   runModels,
		"operational":              runOperational,
		"plan":                     runPlan,
		"sync":                     runSync,
		"sentinel":                 runSentinel,
		"worktree":                 runWorktree,
	}
}

// fleetCommandTable holds the fleet, forge and delivery commands (see printFleetCommands).
func fleetCommandTable() map[string]commandFunc {
	return map[string]commandFunc{
		"gc":          runGC,
		"editors":     runEditors,
		"clients":     runClients,
		"forge":       runForge,
		"harvest":     runHarvest,
		"adopt":       runAdopt,
		"conform":     runAdopt,
		"bootstrap":   runAdopt,
		"dogfood":     runDogfood,
		"bump":        runBump,
		"paperclip":   runPaperclip,
		"changelog":   runChangelog,
		"release":     runRelease,
		"gate":        runGate,
		"agent":       runAgent,
		"serve":       runServe,
		"sbom":        runSBOM,
		"provenance":  runProvenance,
		"needs":       runNeeds,
		"issue":       runIssue,
		"wishes":      runWishes,
		"milestone":   runMilestone,
		"project":     runProject,
		"build":       runBuild,
		"ci":          runCI,
		"topology":    runTopology,
		"workstation": runWorkstation,
		"version":     runVersion,
		"help":        runHelp,
		"-h":          runHelp,
		"--help":      runHelp,
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

// isHelpToken reports whether tok is one of the help spellings a subcommand's raw first
// argument may carry (BUG-811). A subcommand matches this before any flag.Parse call --
// tok is never a parsed Go flag at that point -- so "-h"/"--help"/"help" can be answered
// with the subcommand's own usage text and an exit-0 return instead of falling through to
// "unknown <subcommand> command: <tok>". editors and notebook both need this same check;
// sharing it keeps their help-token spellings from drifting apart (HISS-19).
func isHelpToken(tok string) bool {
	return tok == "-h" || tok == "--help" || tok == "help"
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
