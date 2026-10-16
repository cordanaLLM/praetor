package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ruleregistry"
)

const (
	rulesAuditTimeout       = planTimeout
	maxReportRules          = 256
	maxReportProblems       = 256
	maxRuleForgeComparisons = 32
)

func runRules(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printRulesUsage()
		return nil
	}
	if args[0] != "audit" {
		return fmt.Errorf("unknown rules subcommand: %s", args[0])
	}
	return runRulesAudit(args[1:])
}

func printRulesUsage() {
	fmt.Println("Usage: standardsctl rules audit [--json] [--root <dir>] [--remote]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  audit  Resolve declared enforcement evidence for HISS and Operational Rules")
}

func runRulesAudit(args []string) error {
	return runRulesAuditWithReader(args, nil)
}

type rulesAuditFlags struct {
	root       string
	jsonOutput bool
	remote     bool
	remoteOpts remoteSyncOptions
}

func runRulesAuditWithReader(args []string, injected protectionReader) error {
	flags, err := parseRulesAuditFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(rulesAuditTimeout)
	defer cancel()
	opts := ruleregistry.AuditOptions{Forge: rulesForgeProvider(flags, injected)}
	report, auditErr := ruleregistry.AuditWithOptions(ctx, flags.root, opts)
	if report != nil {
		if printErr := printRulesReport(report, flags.jsonOutput); printErr != nil {
			return printErr
		}
	}
	return auditErr
}

func parseRulesAuditFlags(args []string) (rulesAuditFlags, error) {
	fs := flag.NewFlagSet("rules audit", flag.ContinueOnError)
	root := fs.String("root", ".", "Repository root to inspect")
	jsonOutput := fs.Bool("json", false, "Write the report as JSON")
	remote := fs.Bool("remote", false, "Read live branch protection and compare forge evidence")
	remoteOpts := addRemoteFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return rulesAuditFlags{}, err
	}
	if fs.NArg() > 0 {
		return rulesAuditFlags{}, fmt.Errorf("rules audit accepts no positional arguments, got %q", fs.Args())
	}
	return rulesAuditFlags{root: *root, jsonOutput: *jsonOutput, remote: *remote, remoteOpts: remoteOpts()}, nil
}

func rulesForgeProvider(flags rulesAuditFlags, injected protectionReader) ruleregistry.ForgeProvider {
	return func(ctx context.Context) (map[ruleregistry.ForgeSetting]ruleregistry.ForgeState, error) {
		return resolveRulesForge(ctx, flags, injected)
	}
}

func resolveRulesForge(
	ctx context.Context,
	flags rulesAuditFlags,
	injected protectionReader,
) (map[ruleregistry.ForgeSetting]ruleregistry.ForgeState, error) {
	configPath := filepath.Join(flags.root, config.ManifestFileName)
	manifest, err := config.LoadManifest(configPath)
	if err != nil {
		return nil, fmt.Errorf("load forge assertions: %w", err)
	}
	policy, _, err := planEffectivePolicy(ctx, configPath, "", manifest)
	if err != nil {
		return nil, fmt.Errorf("resolve forge assertions: %w", err)
	}
	target, err := liveProtectionTarget(ctx, flags.root, manifest, policy.BranchProtection)
	if err != nil {
		return nil, fmt.Errorf("resolve forge assertion target: %w", err)
	}
	states, err := assertedForgeStates(target)
	if err != nil || !flags.remote {
		return states, err
	}
	reader, err := rulesProtectionReader(ctx, flags, manifest, injected)
	if err != nil {
		return states, err
	}
	_, findings, err := compareLiveProtection(ctx, reader, target)
	if err != nil {
		return states, err
	}
	return observedForgeStates(states, findings), nil
}

func rulesProtectionReader(
	ctx context.Context,
	flags rulesAuditFlags,
	manifest *config.Manifest,
	injected protectionReader,
) (protectionReader, error) {
	var token string
	if injected == nil {
		token = resolveSyncToken(flags.remoteOpts.token)
		if token == "" {
			return nil, ErrRemoteTokenMissing
		}
	}
	if err := verifyRemoteRepository(ctx, flags.root, manifest.Repository, flags.remoteOpts.host); err != nil {
		return nil, err
	}
	if injected != nil {
		return injected, nil
	}
	driver := forge.NewGitHubDriver(token, flags.remoteOpts.endpoint)
	driver.SetRepository(manifest.Repository.Owner, manifest.Repository.Name)
	return driver, nil
}

func assertedForgeStates(target protectionTarget) (map[ruleregistry.ForgeSetting]ruleregistry.ForgeState, error) {
	empty := &forge.LiveBranchProtection{Branch: target.branch}
	findings, err := forge.EvaluateBranchProtection(target.policy, target.contexts, empty)
	if err != nil {
		return nil, fmt.Errorf("evaluate committed forge assertions: %w", err)
	}
	states := map[ruleregistry.ForgeSetting]ruleregistry.ForgeState{}
	for i := 0; i < len(findings); i++ {
		setting, ok := ruleForgeSetting(findings[i].Property)
		if ok {
			states[setting] = ruleregistry.ForgeState{Asserted: findings[i].Declared,
				AssertedEnforced: findings[i].Verdict == forge.ProtectionDrift}
		}
	}
	return states, nil
}

func observedForgeStates(
	states map[ruleregistry.ForgeSetting]ruleregistry.ForgeState,
	findings []forge.ProtectionFinding,
) map[ruleregistry.ForgeSetting]ruleregistry.ForgeState {
	for i := 0; i < len(findings); i++ {
		setting, ok := ruleForgeSetting(findings[i].Property)
		if !ok {
			continue
		}
		state := states[setting]
		state.Observed = findings[i].Live
		state.Match = findings[i].Verdict != forge.ProtectionDrift
		state.ReadBack = true
		states[setting] = state
	}
	return states
}

func ruleForgeSetting(property string) (ruleregistry.ForgeSetting, bool) {
	switch property {
	case "Signed commits":
		return ruleregistry.ForgeRequiredSignatures, true
	case "Linear history":
		return ruleregistry.ForgeLinearHistory, true
	case "Approving reviews":
		return ruleregistry.ForgeRequiredReviews, true
	case "Required status checks":
		return ruleregistry.ForgeRequiredChecks, true
	default:
		return "", false
	}
}

func printRulesReport(report *ruleregistry.Report, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Println("=== Rule enforcement audit ===")
	for i := 0; i < len(report.Rows) && i < maxReportRules; i++ {
		row := report.Rows[i]
		fmt.Printf("%s %s %s\n", row.ID, row.Class, row.Evidence)
	}
	printForgeComparisons(report)
	printInstructionOnly(report)
	printRulesSummary(report)
	return nil
}

func printForgeComparisons(report *ruleregistry.Report) {
	printedRemote, printedOffline := false, false
	for i := 0; i < len(report.Rows) && i < maxReportRules; i++ {
		for j := 0; j < len(report.Rows[i].Forge) && j < maxRuleForgeComparisons; j++ {
			comparison := report.Rows[i].Forge[j]
			if !comparison.ReadBack {
				if !printedOffline {
					fmt.Println("asserted, not read back:")
					printedOffline = true
				}
				fmt.Printf("  %s forge:%s asserted %s\n", report.Rows[i].ID, comparison.Setting, comparison.Asserted)
				continue
			}
			if !printedRemote {
				fmt.Println("forge comparisons:")
				printedRemote = true
			}
			fmt.Printf("  %s forge:%s asserted %s, observed %s, match=%t\n", report.Rows[i].ID,
				comparison.Setting, comparison.Asserted, comparison.Observed, comparison.Match)
		}
	}
}

func printInstructionOnly(report *ruleregistry.Report) {
	printed := false
	for i := 0; i < len(report.Rows) && i < maxReportRules; i++ {
		if !report.Rows[i].Candidate {
			continue
		}
		if !printed {
			fmt.Println("instruction-only prohibitions:")
			printed = true
		}
		fmt.Printf("  %s\n", report.Rows[i].ID)
	}
}

func printRulesSummary(report *ruleregistry.Report) {
	counts := report.Counts()
	classes := []ruleregistry.Class{
		ruleregistry.ClassCIGate, ruleregistry.ClassHook, ruleregistry.ClassReviewGate,
		ruleregistry.ClassGuidance, ruleregistry.ClassStaged,
	}
	parts := make([]string, 0, len(classes))
	for i := 0; i < len(classes); i++ {
		parts = append(parts, fmt.Sprintf("%s=%d", classes[i], counts[classes[i]]))
	}
	fmt.Printf("classes: %s\n", strings.Join(parts, ", "))
	fmt.Printf("unenforced: %d\n", len(report.UnenforcedIDs()))
	if len(report.Problems) == 0 {
		return
	}
	for i := 0; i < len(report.Problems) && i < maxReportProblems; i++ {
		fmt.Printf("invalid: %s\n", report.Problems[i])
	}
}
