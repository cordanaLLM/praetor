package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/flavor"
)

func runFlavor(args []string) error {
	if len(args) == 0 {
		printFlavorUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list":
		return runFlavorList()
	case "inspect":
		return runFlavorInspect(subArgs)
	case "audit":
		return runFlavorAudit(subArgs)
	case "apply":
		return runFlavorApply(subArgs)
	case "-h", "--help", "help":
		printFlavorUsage()
		return nil
	default:
		return fmt.Errorf("unknown flavor subcommand: %s", sub)
	}
}

func printFlavorUsage() {
	fmt.Println("Usage: praetorctl flavor <subcommand> [args]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  list                           List all supported repository engineering flavors")
	fmt.Println("  inspect <flavor>               Inspect templates, settings, and toolchains for a flavor")
	fmt.Println("  audit [dir] [--flavor=name]    Audit a repository against its flavor requirements")
	fmt.Println("  apply [dir] [--flavor=name]    Scaffold a flavor's templates and branch ruleset; list settings other commands write")
}

func runFlavorList() error {
	flavors := flavor.List()
	fmt.Println("=== Praetor Engineering Flavors ===")
	for _, f := range flavors {
		fmt.Printf("  %-22s : %-45s [HISS: %s]\n", f.Name(), f.Description(), f.HISSProfile())
	}
	return nil
}

func runFlavorInspect(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: praetorctl flavor inspect <flavor-name>")
	}
	flv, err := flavor.Get(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("=== Flavor: %s ===\n", flv.Name())
	fmt.Printf("Description:  %s\n", flv.Description())
	fmt.Printf("HISS Profile: %s\n\n", flv.HISSProfile())

	fmt.Println("Required Templates:")
	for _, t := range flv.RequiredTemplates() {
		printInspectTemplate(t)
	}

	fmt.Println("\nRequired Settings:")
	for _, s := range flv.RequiredSettings() {
		fmt.Printf("  - %-25s : %s\n", s.Name, s.Path)
		if s.Producer != "" {
			fmt.Printf("    %-25s   written by: %s\n", "", s.Producer)
		}
	}

	fmt.Println("\nRequired Toolchains:")
	for _, tc := range flv.RequiredToolchains() {
		printInspectToolchain(tc)
	}
	return nil
}

// printInspectToolchain prints one required toolchain, the binaries that also satisfy it, and
// the repositories it applies to when it does not apply to all (flavor.ToolchainItem.Markers).
func printInspectToolchain(tc flavor.ToolchainItem) {
	fmt.Printf("  - %-15s : %s\n", tc.Binary, tc.Purpose)
	if len(tc.AltBinaries) > 0 {
		fmt.Printf("    %-15s   or: %s\n", "", strings.Join(tc.AltBinaries, ", "))
	}
	if tc.ProjectLocal {
		fmt.Printf("    %-15s   (project-local node_modules/.bin accepted)\n", "")
	}
	if len(tc.Markers) > 0 {
		fmt.Printf("    %-15s   only where: %s\n", "", strings.Join(tc.Markers, ", "))
	}
}

// printInspectTemplate prints one required template, its producer and the other names it is
// accepted under.
func printInspectTemplate(t flavor.TemplateItem) {
	fmt.Printf("  - %-30s (%s)\n", t.Path, t.Description)
	if t.Producer != "" {
		fmt.Printf("    %-30s written by: %s\n", "", t.Producer)
	}
	if len(t.AltPaths) > 0 {
		fmt.Printf("    %-30s or: %s\n", "", strings.Join(t.AltPaths, ", "))
	}
	if t.Search != nil {
		fmt.Printf("    %-30s %s reads the first of: %s\n", "", t.Search.Tool, strings.Join(t.Search.Names, ", "))
	}
}

func runFlavorAudit(args []string) error {
	fs := flag.NewFlagSet("flavor audit", flag.ContinueOnError)
	targetFlv := fs.String("flavor", "auto", "Target flavor (default: the .standards.yaml flavors pin, else auto-detect)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	dir := positionalAt(positional, 0, ".")

	reports, err := auditFlavorReports(dir, *targetFlv)
	if errors.Is(err, flavor.ErrFlavorNotApplicable) {
		fmt.Printf("=== Flavor Audit: %s ===\n  Not applicable: %v\n", dir, err)
		fmt.Println("  The declared profile governs this repository; no flavor describes its stack.")
		fmt.Println("  Pass --flavor=<name> to audit against one anyway.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("flavor audit failed: %w%s", err, explicitFlavorHint(err))
	}

	var failed []string
	for _, report := range reports {
		printFlavorAuditReport(dir, report)
		if !report.Passed {
			failed = append(failed, fmt.Sprintf("%s at %s (score: %.1f%%)", report.Flavor, report.Path, report.Score))
		}
	}
	if len(reports) == 1 && len(failed) == 1 {
		return fmt.Errorf("flavor audit failed (score: %.1f%%)", reports[0].Score)
	}
	if len(failed) > 0 {
		return fmt.Errorf("flavor audit failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

// auditFlavorReports audits dir against the explicit flavor, or, for "" and "auto", against
// every flavor ResolveTargets names: the manifest's flavors pins, else the detected one.
func auditFlavorReports(dir, targetFlv string) ([]*flavor.FlavorAuditReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), flavor.DefaultAuditTimeout)
	defer cancel()
	if targetFlv != "" && targetFlv != "auto" {
		report, err := flavor.AuditFlavorContext(ctx, dir, targetFlv)
		if err != nil {
			return nil, err
		}
		return []*flavor.FlavorAuditReport{report}, nil
	}
	return flavor.AuditTargetsContext(ctx, dir)
}

// printFlavorAuditReport prints one report; a pinned flavor also names the directory it was
// audited against.
func printFlavorAuditReport(dir string, report *flavor.FlavorAuditReport) {
	scope := ""
	if report.Path != "" && report.Path != "." {
		scope = ", Path: " + report.Path
	}
	fmt.Printf("=== Flavor Audit: %s (Flavor: %s%s) ===\n", dir, report.Flavor, scope)
	fmt.Printf("  Score:       %.1f%%\n", report.Score)
	fmt.Printf("  Passed:      %v\n", report.Passed)
	fmt.Printf("  Templates:   %d/%d present\n", report.TemplatesPresent, report.TemplatesTotal)
	fmt.Printf("  Settings:    %d/%d valid\n", report.SettingsValid, report.SettingsTotal)
	fmt.Printf("  Toolchains:  %d/%d available (advisory, not scored)\n", report.ToolchainsAvailable, report.ToolchainsTotal)
	printFlavorAuditFindings(report)
}

// printFlavorAuditFindings prints each missing template, missing or invalid setting, missing
// toolchain and shadowed template a flavor audit found, one block per kind, nothing for a
// kind with no entries.
func printFlavorAuditFindings(report *flavor.FlavorAuditReport) {
	if len(report.MissingTemplates) > 0 {
		fmt.Println("\nMissing Templates:")
		for _, t := range report.MissingTemplates {
			fmt.Printf("  - %s (%s)\n", t.Path, t.Description)
		}
	}
	// Invalid settings used to be counted and never named, so an operator saw the score drop
	// with nothing to act on.
	if len(report.MissingSettings) > 0 {
		fmt.Println("\nMissing or Invalid Settings:")
		for _, s := range report.MissingSettings {
			fmt.Printf("  - %s : %s (%s)\n", s.Name, s.Path, s.Description)
		}
	}
	if len(report.MissingToolchains) > 0 {
		fmt.Println("\nMissing Toolchains (advisory):")
		for _, tc := range report.MissingToolchains {
			fmt.Printf("  - %s : %s (Install: %s)\n", tc.Binary, tc.Purpose, tc.InstallGuide)
		}
	}
	// A shadowed file looks like configuration and is never read, so an edit to it changes
	// nothing; naming the file in use is the whole point.
	if len(report.ShadowedTemplates) > 0 {
		fmt.Println("\nShadowed Templates (advisory):")
		for _, s := range report.ShadowedTemplates {
			fmt.Printf("  - %s reads %s; ignored: %s\n", s.Tool, s.InUse, strings.Join(s.Ignored, ", "))
		}
	}
}

func runFlavorApply(args []string) error {
	fs := flag.NewFlagSet("flavor apply", flag.ContinueOnError)
	targetFlv := fs.String("flavor", "auto", "Target flavor (default: auto-detect)")
	force := fs.Bool("force", false, "Overwrite existing templates and a branch ruleset that differs from the rendered one")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	dir := positionalAt(positional, 0, ".")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ApplyFlavor seeds the private ledger, so Git must be made to ignore what it wrote.
	return withLedgerIgnore(ctx, dir, func() error { return applyFlavor(ctx, dir, *targetFlv, *force) })
}

// explicitFlavorHint is the remedy flavor audit and flavor apply add to a nothing-matched
// refusal. They take --flavor; gate run, which surfaces the same flavor.ErrNoFlavorMatched, does
// not, so the hint is added here rather than carried by the sentinel (#615).
func explicitFlavorHint(err error) string {
	if errors.Is(err, flavor.ErrNoFlavorMatched) {
		return "; pass an explicit --flavor=<name> (praetorctl flavor list names each)"
	}
	return ""
}

// applyFlavor scaffolds one flavor and prints what it created, skipped and failed. The branch
// ruleset is not written where the repository's adoption.decline names branch-ruleset, resolved
// by adoption's own decline parser.
func applyFlavor(ctx context.Context, dir, targetFlv string, force bool) error {
	report, err := flavor.ApplyFlavorWith(ctx, dir, targetFlv, flavor.ApplyOptions{
		Force: force,
		Declines: func(ctx context.Context, step string) (bool, error) {
			return adopt.RepositoryArtifactDeclined(ctx, dir, step)
		},
	})
	if report == nil {
		return fmt.Errorf("flavor apply failed: %w%s", err, explicitFlavorHint(err))
	}
	// A report beside an error (flavor.ErrApplyIncomplete) still names what was written, so it
	// is printed before the failure is returned.
	printFlavorApplyReport(dir, report)
	if err != nil {
		return fmt.Errorf("flavor apply failed: %w", err)
	}
	return nil
}

// printFlavorApplyReport prints what one flavor apply created, refreshed, skipped, deferred,
// held back and failed.
func printFlavorApplyReport(dir string, report *flavor.ApplyReport) {
	fmt.Printf("=== Applied Flavor: %s to %s ===\n", report.Flavor, dir)
	fmt.Printf("  Created Templates (%d): %s\n", len(report.CreatedTemplates), strings.Join(report.CreatedTemplates, ", "))
	printApplyEntries("Refreshed Earlier Praetor Text", report.RefreshedTemplates)
	if len(report.SkippedTemplates) > 0 {
		fmt.Printf("  Skipped Existing  (%d): %s\n", len(report.SkippedTemplates), strings.Join(report.SkippedTemplates, ", "))
	}
	if len(report.DeferredTemplates) > 0 {
		fmt.Printf("  Left to Producer  (%d): %s\n", len(report.DeferredTemplates), strings.Join(report.DeferredTemplates, ", "))
	}
	printApplyEntries("Kept Existing Config", coveredEntries(report.CoveredTemplates))
	// One per line: each entry names a path and what the repository lacks for its body.
	printApplyEntries("Unmet Requirement", report.UnmetTemplates)
	printApplyEntries("Settings", settingEntries(report.Settings))
	fmt.Printf("  WorkingDir State:     %v\n", report.WorkingDirCreated)

	printApplyEntries("Errors", report.Errors)
}

// coveredEntries renders each template apply left to the configuration already in use as
// "<file in use> (<name not written>)".
func coveredEntries(covered []flavor.CoveredTemplate) []string {
	entries := make([]string, 0, len(covered))
	for _, c := range covered {
		entries = append(entries, fmt.Sprintf("%s (%s not written)", c.InUse, c.Path))
	}
	return entries
}

// settingEntries renders each required setting apply handled as "<path>: <action>", with the
// note in parentheses: the producer of a deferred setting, what differs in a kept one.
func settingEntries(settings []flavor.SettingOutcome) []string {
	entries := make([]string, 0, len(settings))
	for _, s := range settings {
		entry := fmt.Sprintf("%s: %s", s.Path, s.Action)
		if s.Note != "" {
			entry += " (" + s.Note + ")"
		}
		entries = append(entries, entry)
	}
	return entries
}

// printApplyEntries prints a labelled count and one entry per line, and nothing when empty.
func printApplyEntries(label string, entries []string) {
	if len(entries) == 0 {
		return
	}
	fmt.Printf("  %s (%d):\n", label, len(entries))
	for _, entry := range entries {
		fmt.Printf("    - %s\n", entry)
	}
}
