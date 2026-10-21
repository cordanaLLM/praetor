package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/dogfood"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/needs"
)

// agentUsage describes the agent command-line syntax.
const agentUsage = "usage: praetorctl agent <list|run <agent-name>|sandbox [render [--part full|network|namespaces]] [--lane NAME] [--allow-loopback-port N] [--workspace DIR] [--read-only] -- COMMAND [ARGS...]>"

// printAgentUsage answers a help probe of the agent command.
func printAgentUsage() {
	fmt.Println(agentUsage)
	fmt.Println("Agent helpers take no arguments; 'praetorctl agent list' names them.")
	fmt.Println("'praetorctl agent run praetor-gatekeeper' runs the full gate, the same pipeline as 'praetorctl gate run'.")
	fmt.Println("'praetorctl agent sandbox' runs one command in a Linux sandbox; unavailable isolation is an error.")
}

// runAgent dispatches the agent subcommands: list and run. A help token anywhere in args
// (isHelpToken, the detection gate uses) prints usage and returns flag.ErrHelp before any
// helper starts, so a `--help` probe of `agent run praetor-gatekeeper` never reaches the
// gating pipeline and exits 0 like `gate --help` (BUG-790).
func runAgent(args []string) error {
	if len(args) > 0 && args[0] == "sandbox" {
		return runAgentSandbox(args[1:])
	}
	if slices.ContainsFunc(args, isHelpToken) {
		printAgentUsage()
		return flag.ErrHelp
	}
	if len(args) == 0 || args[0] == "list" {
		return listAgents()
	}

	if args[0] == "run" {
		if len(args) < 2 {
			return fmt.Errorf("usage: praetorctl agent run <agent-name>")
		}
		return dispatchAgentTask(args[1], args[2:])
	}

	return fmt.Errorf("unknown agent subcommand: %s (available: list, run, sandbox)", args[0])
}

// agentHelper is one helper 'agent run' can run: its name and its implementation.
type agentHelper struct {
	name string
	run  func(context.Context) error
}

// agentHelpers is the one registry of runnable helpers: 'agent run' dispatches from it and
// 'agent list' offers exactly these as runnable, so the list never names a helper the run
// refuses (#1277, HISS-19). A persona file under .agents/agents without an entry here is
// persona only: the list names it separately as not runnable, and the run refuses it by name.
var agentHelpers = []agentHelper{
	{name: "praetor-auditor", run: runAuditorAgent},
	{name: "praetor-dogfooder", run: runDogfooderAgent},
	{name: "praetor-gatekeeper", run: runGatekeeperAgent},
	{name: "praetor-needs-miner", run: runNeedsMinerAgent},
}

// canonicalAgentName is name in the dashed form the registry and the persona files use;
// 'agent run' also accepts the underscored form.
func canonicalAgentName(name string) string {
	return strings.ReplaceAll(name, "_", "-")
}

// findAgentHelper returns the registry entry for name, in either form.
func findAgentHelper(name string) (agentHelper, bool) {
	canonical := canonicalAgentName(name)
	for i := 0; i < len(agentHelpers); i++ {
		if agentHelpers[i].name == canonical {
			return agentHelpers[i], true
		}
	}
	return agentHelper{}, false
}

// personaOnlyAgents returns the persona names under .agents/agents in the working directory,
// read through the walk compile-context projects from (compiler.ReadAgentInventory), that the
// registry cannot run. A directory without personas holds none.
func personaOnlyAgents(ctx context.Context) ([]string, error) {
	inventory, err := compiler.ReadAgentInventory(ctx, ".")
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", compiler.CanonicalAgentsRel, err)
	}
	var names []string
	for i := 0; i < len(inventory.Personas); i++ {
		name := strings.TrimSuffix(inventory.Personas[i], ".md")
		if _, runnable := findAgentHelper(name); !runnable {
			names = append(names, name)
		}
	}
	return names, nil
}

// listAgents names the helpers 'agent run' can run, from the registry, with the persona file
// of each where the repository holds one, and then, apart, the persona files no helper
// implements.
func listAgents() error {
	ctx, cancel := commandContext(agentTimeout)
	defer cancel()
	personaOnly, err := personaOnlyAgents(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("=== Available Praetor Autonomous Agent Helpers ===\n\n")
	for i := 0; i < len(agentHelpers); i++ {
		fmt.Printf("  • %-25s (%s)\n", agentHelpers[i].name, agentPersonaSource(agentHelpers[i].name))
	}
	if len(personaOnly) > 0 {
		fmt.Printf("\nPersona only, not runnable by 'agent run' (no implementation):\n\n")
		for i := 0; i < len(personaOnly); i++ {
			fmt.Printf("  • %-25s (%s/%s.md)\n", personaOnly[i], compiler.CanonicalAgentsRel, personaOnly[i])
		}
	}
	fmt.Printf("\nExecute an agent helper: praetorctl agent run <agent-name>\n")
	return nil
}

// agentPersonaSource names the persona file of the helper name in the working directory, or
// says that the helper runs without one.
func agentPersonaSource(name string) string {
	rel := compiler.CanonicalAgentsRel + "/" + name + ".md"
	if info, err := os.Lstat(filepath.FromSlash(rel)); err == nil && info.Mode().IsRegular() {
		return rel
	}
	return "built in, no persona file here"
}

func dispatchAgentTask(agentName string, extraArgs []string) error {
	// extraArgs carried no meaning until now (BUG-726): the parameter was never read, so
	// a typo'd or unsupported flag after the agent name was silently dropped instead of
	// refused.
	if len(extraArgs) != 0 {
		return fmt.Errorf("agent run %s: unexpected extra argument(s) %v (agent helpers take no arguments)", agentName, extraArgs)
	}
	helper, runnable := findAgentHelper(agentName)
	if !runnable {
		return refuseAgentPersona(agentName)
	}

	ctx, cancel := agentContext(agentName)
	defer cancel()

	fmt.Printf("[Agent Dispatch] Activating autonomous agent helper: %s\n", agentName)
	return helper.run(ctx)
}

// refuseAgentPersona is the error for a name the registry cannot run: a persona file with no
// implementation is refused by name, as 'agent list' marks it, and anything else is unknown.
func refuseAgentPersona(agentName string) error {
	ctx, cancel := commandContext(agentTimeout)
	defer cancel()
	personaOnly, err := personaOnlyAgents(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(personaOnly, canonicalAgentName(agentName)) {
		return fmt.Errorf("agent persona %s has a persona file but no implementation 'agent run' can run; "+
			"'praetorctl agent list' names it as not runnable", agentName)
	}
	return fmt.Errorf("unknown agent persona: %s (run 'praetorctl agent list' to view available agents)", agentName)
}

// agentTimeout bounds every agent helper except the gatekeeper.
const agentTimeout = 5 * time.Minute

// gatekeeperRepoDir is the repository the gatekeeper gates: the working directory. Its deadline
// is sized to the same directory, so a repository holding a Cargo.lock gets the Cargo budget.
const gatekeeperRepoDir = "."

// agentContext bounds one agent helper. The gatekeeper runs the full gating pipeline, so it takes
// the gate run's own deadline: a fixed five minutes cut its race stage short (#314).
func agentContext(agentName string) (context.Context, context.CancelFunc) {
	if isGatekeeperAgent(agentName) {
		return gating.PlanRun(gatekeeperRepoDir, false).WithDeadline(rootContext())
	}
	return commandContext(agentTimeout)
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

// runGatekeeperAgent runs the full gate the way `gate run` does and fails the same way: a
// REJECTED pipeline is an error, so the helper exits 1 instead of reporting the rejection
// under exit 0. The stage report prints first, so the failing stage and its reason stay
// visible.
func runGatekeeperAgent(ctx context.Context) error {
	rep, err := gatedPipeline(ctx, gatekeeperRepoDir, gating.RunOptions{})
	if err != nil {
		return fmt.Errorf("gatekeeper execution error: %w", err)
	}
	printGatingReport(rep)
	fmt.Printf("[praetor-gatekeeper] Gated pipeline completed: %s\n", rep.Status)
	if rep.Status == gating.StatusRejected {
		return errors.New("gatekeeper rejection: repository rejected by gating pipeline")
	}
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

// runNeedsMinerAgent scans the working directory against the operator's framework targets,
// selected through the environment and the install manifest like hook (the agent command
// takes no settings flags).
func runNeedsMinerAgent(ctx context.Context) error {
	selection, err := loadNeedsSelection(ctx, defaultOperatorSettingsFlags())
	if err != nil {
		return fmt.Errorf("needs miner error: %w", err)
	}
	rep, err := needs.ScanRepo(ctx, ".", selection.registry)
	if err != nil {
		return fmt.Errorf("needs miner error: %w", err)
	}
	fmt.Printf("[praetor-needs-miner] Scan complete: Readiness %s (%s)\n",
		needs.MappingAvailability(rep.Readiness), needs.FormatDependencyCounts(rep.Readiness))
	return nil
}
