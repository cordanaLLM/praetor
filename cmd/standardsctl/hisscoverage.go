package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/hisscoverage"
)

// maxCoverageFindingsPrinted bounds the report so a wholesale drift does not bury the
// summary that explains it (HISS-02).
const maxCoverageFindingsPrinted = 40

// runHissCoverage reports the declared enforcement evidence and, with --verify, replays the
// fixture corpus that backs it.
func runHissCoverage(args []string) error {
	fs := flag.NewFlagSet("hiss coverage", flag.ContinueOnError)
	root := fs.String("path", ".", "Repository root to inspect")
	verify := fs.Bool("verify", false, "Replay the fixture corpus and fail when a claim is not supported")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	catalog, err := hisscoverage.LoadCatalog(ctx, *root)
	if err != nil {
		if errors.Is(err, hisscoverage.ErrCatalogAbsent) {
			fmt.Printf("=== HISS Coverage: not declared ===\n")
			fmt.Printf("  No catalog at %s; enforcement evidence is undeclared for every invariant.\n",
				hisscoverage.CatalogFile)
		}
		return err
	}

	printCoverageSummary(catalog)
	if !*verify {
		return nil
	}
	return verifyCoverage(ctx, *root, catalog)
}

// printCoverageSummary reports what the catalog claims, without implying it was checked.
func printCoverageSummary(catalog *hisscoverage.Catalog) {
	fmt.Printf("=== HISS Coverage: %d invariant(s) declared ===\n", len(catalog.Rules))
	for _, id := range catalog.RuleIDs() {
		printRuleCoverage(catalog, id)
	}
	counts := catalog.Summary()
	fmt.Printf("\n  Declared: enforced=%d partial=%d unsupported=%d not_applicable=%d manual=%d\n",
		counts[hisscoverage.StateEnforced], counts[hisscoverage.StatePartial],
		counts[hisscoverage.StateUnsupported], counts[hisscoverage.StateNotApplicable],
		counts[hisscoverage.StateManual])
}

// printRuleCoverage prints one invariant's declared evidence.
func printRuleCoverage(catalog *hisscoverage.Catalog, id string) {
	for i := 0; i < len(catalog.Rules); i++ {
		rule := catalog.Rules[i]
		if rule.ID != id {
			continue
		}
		fmt.Printf("  %s %s\n", rule.ID, rule.Title)
		for j := 0; j < len(rule.Coverage); j++ {
			cov := rule.Coverage[j]
			fmt.Printf("    %-11s %-15s %s\n", cov.Language, cov.State, cov.Mechanism)
		}
	}
}

// verifyCoverage replays the corpus and fails when the evidence contradicts a claim.
func verifyCoverage(ctx context.Context, root string, catalog *hisscoverage.Catalog) error {
	report, err := hisscoverage.Verify(ctx, root, catalog)
	if err != nil {
		return err
	}
	for i := 0; i < len(report.Unbacked) && i < maxCoverageFindingsPrinted; i++ {
		fmt.Printf("  [WARN] %s\n", report.Unbacked[i])
	}
	if len(report.Delegated) > 0 {
		fmt.Printf("\n  %d claim(s) are decided by another tool; their attribution is checked here, not their enforcement:\n",
			len(report.Delegated))
		for i := 0; i < len(report.Delegated) && i < maxCoverageFindingsPrinted; i++ {
			fmt.Printf("    [INFO] %s\n", report.Delegated[i])
		}
	}
	if !report.Passed() {
		fmt.Printf("\n=== Coverage claims contradicted by their fixtures ===\n")
		for i := 0; i < len(report.Findings) && i < maxCoverageFindingsPrinted; i++ {
			fmt.Printf("  [FAIL] %s\n", report.Findings[i])
		}
		if len(report.Findings) > maxCoverageFindingsPrinted {
			fmt.Printf("  ... and %d more\n", len(report.Findings)-maxCoverageFindingsPrinted)
		}
		return fmt.Errorf("%w: %d of %d claim(s) contradicted across %d fixture(s)",
			hisscoverage.ErrClaimUnsupported, len(report.Findings), report.Claims, report.Fixtures)
	}
	fmt.Printf("\n[PASS] %d claim(s) checked against %d fixture(s) (%d replayed here, %d attributed elsewhere); every claim holds.\n",
		report.Claims, report.Fixtures, report.Claims-len(report.Delegated), len(report.Delegated))
	return nil
}

// runHiss dispatches the hiss subcommands.
func runHiss(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: praetorctl hiss <coverage> [--path=.] [--verify]")
		return nil
	}
	switch args[0] {
	case "coverage":
		return runHissCoverage(args[1:])
	default:
		return fmt.Errorf("unknown hiss subcommand: %s", args[0])
	}
}
