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
// fixture corpus that backs it. With --sync-titles it rewrites the coverage file's stale rule
// titles instead (syncCoverageTitles).
func runHissCoverage(args []string) error {
	fs := flag.NewFlagSet("hiss coverage", flag.ContinueOnError)
	root := fs.String("path", ".", "Repository root to inspect")
	verify := fs.Bool("verify", false, "Replay the fixture corpus and fail when a claim is not supported")
	syncTitles := fs.Bool("sync-titles", false,
		"Rewrite each rule title that differs from the HISS catalog to the catalog's text; a dry run unless --write is given")
	write := fs.Bool("write", false, "With --sync-titles, rewrite "+hisscoverage.CatalogFile+" in place")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if err := checkCoverageModes(*verify, *syncTitles, *write); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if *syncTitles {
		return syncCoverageTitles(ctx, *root, *write)
	}

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
	printStaleTitles(catalog.StaleTitles())
	if !*verify {
		return nil
	}
	return verifyCoverage(ctx, *root, catalog)
}

// checkCoverageModes refuses a flag combination naming two runs: --write belongs to
// --sync-titles, and a title sync is not a verification.
func checkCoverageModes(verify, syncTitles, write bool) error {
	if write && !syncTitles {
		return errors.New("hiss coverage: --write applies only with --sync-titles")
	}
	if verify && syncTitles {
		return errors.New("hiss coverage: --sync-titles and --verify are separate runs; sync the titles first")
	}
	return nil
}

// printStaleTitles warns about each declared title that differs from the HISS catalog. The
// catalog owns the titles, so the summary above already printed its text, and a stale title
// never fails the run (#844).
func printStaleTitles(stale []hisscoverage.StaleTitle) {
	if len(stale) == 0 {
		return
	}
	fmt.Printf("\n  %d title(s) differ from the HISS catalog, which owns them; the catalog's text is used:\n", len(stale))
	for i := 0; i < len(stale) && i < maxCoverageFindingsPrinted; i++ {
		fmt.Printf("  [WARN] %s\n", stale[i])
	}
	if len(stale) > maxCoverageFindingsPrinted {
		fmt.Printf("  ... and %d more\n", len(stale)-maxCoverageFindingsPrinted)
	}
	fmt.Printf("  Drop each title line, or run 'praetorctl hiss coverage --sync-titles --write' to rewrite them.\n")
}

// syncCoverageTitles rewrites the stale titles of the coverage file below root, or, without
// write, lists what it would rewrite and leaves the file untouched.
func syncCoverageTitles(ctx context.Context, root string, write bool) error {
	result, err := hisscoverage.SyncTitlesFile(ctx, root, write)
	if err != nil {
		return err
	}
	if len(result.Stale) == 0 {
		fmt.Printf("=== HISS Coverage titles: in sync ===\n")
		fmt.Printf("  Every title in %s is omitted or matches the HISS catalog; nothing to rewrite.\n",
			hisscoverage.CatalogFile)
		return nil
	}
	mode := "dry run, nothing written"
	if result.Written {
		mode = "rewritten"
	}
	fmt.Printf("=== HISS Coverage titles: %d stale (%s) ===\n", len(result.Stale), mode)
	for i := 0; i < len(result.Stale) && i < maxCoverageFindingsPrinted; i++ {
		fmt.Printf("  %s %q -> %q\n", result.Stale[i].ID, result.Stale[i].Declared, result.Stale[i].Catalog)
	}
	if len(result.Stale) > maxCoverageFindingsPrinted {
		fmt.Printf("  ... and %d more\n", len(result.Stale)-maxCoverageFindingsPrinted)
	}
	if result.Written {
		fmt.Printf("\n  Rewrote %d title(s) in %s.\n", len(result.Stale), hisscoverage.CatalogFile)
		return nil
	}
	fmt.Printf("\n  Rerun with --sync-titles --write to rewrite them in %s.\n", hisscoverage.CatalogFile)
	return nil
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
		fmt.Printf("  %s %s\n", rule.ID, rule.CatalogTitle())
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

// printHissUsage answers a bare or help-token `hiss` invocation.
func printHissUsage() {
	fmt.Println("Usage: praetorctl hiss <subcommand> [args]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  coverage [--path=.] [--verify]    Report declared HISS enforcement evidence; --verify replays the fixture corpus")
	fmt.Println("  coverage --sync-titles [--write]  Rewrite rule titles that differ from the HISS catalog; a dry run unless --write")
}

// runHiss dispatches the hiss subcommands. The top-level help tells a reader to run
// `<command> -h`, so a help token (isHelpToken) prints usage and succeeds instead of
// being read as an unknown subcommand.
func runHiss(args []string) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printHissUsage()
		return nil
	}
	switch args[0] {
	case "coverage":
		return runHissCoverage(args[1:])
	default:
		return fmt.Errorf("unknown hiss subcommand: %s", args[0])
	}
}
