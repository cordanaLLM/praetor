package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
)

// profileTimeout bounds one profile change, including the git checks of its backups (HISS-02).
const profileTimeout = 2 * time.Minute

// errProfileIncomplete keeps a profile change that recorded errors from returning success.
var errProfileIncomplete = errors.New("profile change completed with errors")

// profileUsage is the one usage text of the profile command.
const profileUsage = "usage: praetorctl profile set [<profile>] [--facets=<id>,...] --lock-source-root=<praetor checkout> " +
	"[--path=.] [--dry-run]"

// runProfile dispatches `praetorctl profile <action>`; set is the one action.
func runProfile(args []string) error {
	if len(args) == 0 {
		return errors.New(profileUsage)
	}
	switch args[0] {
	case "set":
		return runProfileSet(args[1:])
	case "-h", "--help", "help":
		fmt.Println(profileUsage)
		fmt.Println("\nChanges an adopted repository's declared profile or facets, or re-pins them to a newer Praetor")
		fmt.Println("catalog, rewriting only .standards.yaml, .standards.lock and the vendored catalog texts. It then runs")
		fmt.Println("the audit gates that check files derived from the declaration (README block, documentation gate,")
		fmt.Println("DevContainer, branch protection ruleset), leaves those files as they are and names the refresh,")
		fmt.Println("praetorctl adopt --force, when one no longer matches.")
		return nil
	}
	return fmt.Errorf("unknown profile action %q; %s", args[0], profileUsage)
}

// runProfileSet declares a profile or facets in an adopted repository (adopt.SetProfile), prints
// what it wrote, or in a dry run what it would write, and then which files derived from the
// declaration audit now fails on (reportDeclarationGates). A failing gate is reported, not an
// error: profile set wrote what it was asked to, and the refresh is a separate, broader command.
func runProfileSet(args []string) error {
	opts, err := parseProfileSetOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(profileTimeout)
	defer cancel()
	report, err := adopt.SetProfile(ctx, opts)
	if report != nil {
		printProfileSetReport(report)
	}
	if err != nil {
		return fmt.Errorf("profile set failed: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%w: %d error(s) listed above", errProfileIncomplete, len(report.Errors))
	}
	return reportDeclarationGates(ctx, report, opts)
}

// parseProfileSetOptions reads `profile set [<profile>]` and its flags. --facets given, even
// empty, replaces the declared facets; omitted, it keeps them. Neither a profile nor --facets
// re-pins the declaration as it stands.
func parseProfileSetOptions(args []string) (adopt.ProfileSetOptions, error) {
	fs := flag.NewFlagSet("profile set", flag.ContinueOnError)
	facets := fs.String("facets", "", "Comma-separated facets that replace the declared ones; --facets= declares none (omitted: keep the declared facets)")
	lockSource := fs.String("lock-source-root", "", "Praetor source bundle the new pins and vendored catalog texts come from (required)")
	path := fs.String("path", ".", "Adopted repository whose declaration changes")
	dryRun := fs.Bool("dry-run", false, "Preview every change as a diff without writing files")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return adopt.ProfileSetOptions{}, err
	}
	if len(positional) > 1 {
		return adopt.ProfileSetOptions{}, fmt.Errorf("profile set takes at most one profile, got %q; %s", positional, profileUsage)
	}
	opts := adopt.ProfileSetOptions{Path: *path, Profile: positionalAt(positional, 0, ""),
		Facets: splitCommaList(*facets), LockSourceRoot: *lockSource, DryRun: *dryRun}
	fs.Visit(func(set *flag.Flag) {
		if set.Name == "facets" {
			opts.SetFacets = true
		}
	})
	return opts, nil
}

// printProfileSetReport prints the declaration a profile change leaves and each file it wrote,
// verified or would write, with the dry-run diffs, in the sections an adoption report uses.
func printProfileSetReport(rep *adopt.AdoptReport) {
	fmt.Println("=== Praetor Profile Change ===")
	if rep.DryRun {
		fmt.Println("[DRY-RUN SIMULATION: No filesystem mutations performed]")
	}
	if rep.Archetype != "" {
		fmt.Printf("Profile:            %s\n", rep.Archetype)
		fmt.Printf("Facets:             %v\n", rep.Facets)
	}
	printAdoptedFiles(rep)
	printAdoptPreviews(rep.Previews)
	printAdoptIssues(rep)
}

// declarationGate is one audit gate whose verdict follows the declared profiles and facets.
type declarationGate func(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error

// declarationGates are the audit gates that check files adoption derives from the declared
// profiles and facets, each one runAuditGates runs: the README block, the documentation gate,
// the DevContainer and the branch protection ruleset. profile set writes none of those files, so
// it runs these gates against the declaration it leaves.
func declarationGates() []declarationGate {
	return []declarationGate{
		auditReadmeGovernance,
		func(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
			return auditDocumentationGate(ctx, manifest, opts.rootDir, opts.effective.Policy.BranchProtection)
		},
		auditDevContainer,
		func(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
			return auditBranchProtection(ctx, manifest, opts.rootDir, &opts.effective.Policy)
		},
	}
}

// reportDeclarationGates runs declarationGates in the repository against the declaration profile
// set left, or in a dry run would leave (the report's effective policy), and names the refresh
// when one fails (#123).
func reportDeclarationGates(ctx context.Context, rep *adopt.AdoptReport, opts adopt.ProfileSetOptions) error {
	if rep.EffectivePolicy == nil || rep.EffectivePolicy.Manifest == nil {
		return errors.New("profile set reported no effective policy to check the derived files against")
	}
	root, err := filepath.Abs(opts.Path)
	if err != nil {
		return fmt.Errorf("resolve repository path %q: %w", opts.Path, err)
	}
	subject := "the declaration now written"
	if rep.DryRun {
		subject = "the planned declaration"
	}
	fmt.Printf("\nAudit gates for files derived from the declaration, checked against %s:\n", subject)
	failed := checkDeclarationGates(ctx, root, rep.EffectivePolicy)
	if failed == 0 {
		return nil
	}
	when := "fail: praetorctl audit fails"
	if rep.DryRun {
		when = "would fail once profile set runs without --dry-run: praetorctl audit would fail"
	}
	fmt.Printf("\n%d gate(s) %s until those files follow the declaration, and profile set does not write them.\n"+
		"Refresh them with adoption, which also rewrites every other audit-locked file that drifted; preview it first:\n"+
		"  praetorctl adopt --force --dry-run --lock-source-root=%s --path=%s\n", failed, when, opts.LockSourceRoot, opts.Path)
	return nil
}

// checkDeclarationGates runs every declarationGate in root against effective, printing each
// verdict as the audit prints it, and returns how many failed. The README gate reads the
// recorded baseline the audit reads (auditBaselineAndInvariants).
func checkDeclarationGates(ctx context.Context, root string, effective *config.EffectivePolicy) int {
	opts := &auditOptions{rootDir: root, effective: effective}
	failed := 0
	base, err := baseline.LoadBaseline(resolveCompanion(root, "", ".standards-baseline.json"))
	if err != nil {
		fmt.Printf("[FAIL] Baseline audit failed: %v\n", err)
		failed++
	} else {
		opts.baseline, opts.baselineKnown = base, !base.Absent
	}
	gates := declarationGates()
	for i := 0; i < len(gates) && i < maxAuditGates; i++ {
		if err := gates[i](ctx, effective.Manifest, opts); err != nil {
			fmt.Println(err)
			failed++
		}
	}
	return failed
}
