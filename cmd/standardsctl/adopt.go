package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

// adoptTimeout bounds one adoption run, including git and scanner subprocesses.
const adoptTimeout = 2 * time.Minute

// errAdoptIncomplete prevents a partially successful report from returning success.
var errAdoptIncomplete = errors.New("adoption completed with errors")

// resolveHomeSubdir returns explicit when it is set, and otherwise joins segs onto the
// user's home directory. It never degrades to a working-directory-relative path: when the
// home directory cannot be resolved the caller gets an error naming the flag to pass
// instead, rather than silently retargeting the command at the current directory.
func resolveHomeSubdir(explicit, flagName string, segs ...string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory (pass %s explicitly): %w", flagName, err)
	}
	if home == "" {
		return "", fmt.Errorf("home directory is empty: pass %s explicitly", flagName)
	}
	return filepath.Join(append([]string{home}, segs...)...), nil
}

// splitCommaList splits a comma-separated flag value, dropping empty entries.
func splitCommaList(value string) []string {
	if value == "" {
		return nil
	}
	items := make([]string, 0, strings.Count(value, ",")+1)
	for _, f := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func runAdopt(args []string) error {
	fs := flag.NewFlagSet("adopt", flag.ContinueOnError)
	profile := fs.String("profile", "", "Primary repository profile (auto-detected if empty)")
	facets := fs.String("facets", "", "Comma-separated facets written when adoption creates .standards.yaml "+
		"(default when omitted: "+strings.Join(config.DefaultFacets(), ",")+"); an existing .standards.yaml keeps the facets it declares, "+
		"change them with praetorctl profile set --facets")
	dryRun := fs.Bool("dry-run", false, "Simulate adoption without writing files")
	force := fs.Bool("force", false, adopt.ForceContract+". --force needs --lock-source-root, --dry-run included")
	recordBaseline := fs.Bool("record-baseline", true, "Record or estimate legacy debt using verified local pins and catalog, or --lock-source-root (including --dry-run)")
	lockSource := fs.String("lock-source-root", "", "Praetor source bundle with validated pins and local archetypes for missing lockfiles")
	allMissing := fs.Bool("all-missing", false, "Adopt all detected unmanaged repositories under --dev-dir")
	devDir := fs.String("dev-dir", "", "Root directory scanned by --all-missing "+devRootUsageDefault)
	path := fs.String("path", ".", "Target repository path to adopt")
	limitFlags := registerVerificationLimitFlags(fs)

	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	if fs.NArg() > 0 && *path == "." {
		*path = fs.Arg(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), adoptTimeout)
	defer cancel()

	if *allMissing {
		root, err := resolveDevRootDir(*devDir, "--dev-dir")
		if err != nil {
			return fmt.Errorf("adopt --all-missing: %w", err)
		}
		return batchAdoptMissing(ctx, root, *dryRun, *force, *recordBaseline, *lockSource)
	}

	opts := adopt.AdoptOptions{
		Path:               *path,
		LockSourceRoot:     *lockSource,
		Profile:            *profile,
		Facets:             splitCommaList(*facets),
		SetFacets:          flagWasSet(fs, "facets"),
		DryRun:             *dryRun,
		Force:              *force,
		RecordBaseline:     *recordBaseline,
		VerificationLimits: limitFlags.limits(),
	}

	report, err := adopt.Adopt(ctx, opts)
	if report != nil {
		printAdoptReport(report)
	}
	if err != nil {
		return fmt.Errorf("adopt repository failed: %w", err)
	}
	if len(report.Errors) > 0 {
		return fmt.Errorf("%w: %d error(s) listed above", errAdoptIncomplete, len(report.Errors))
	}
	return nil
}

// verificationLimitFlags holds the --verification-max-* flags. `adopt` and `paperclip harness`
// both register them through registerVerificationLimitFlags, so the two commands accept the same
// overrides under the same names and help text (issue #535).
type verificationLimitFlags struct {
	entries, files, depth *int
}

// registerVerificationLimitFlags adds the discovery bound flags to fs. Each defaults to 0, which
// keeps adoption's default for that bound. The names are literals, not adopt.Verification*Flag:
// `praetorctl docs references` reads a flag name only from a string literal, so a constant would
// hide these flags from it. TestRegisterVerificationLimitFlags_NamesMatchAdopt keeps both equal.
func registerVerificationLimitFlags(fs *flag.FlagSet) verificationLimitFlags {
	defaults := adopt.DefaultVerificationLimits()
	return verificationLimitFlags{
		entries: fs.Int("verification-max-entries", 0, fmt.Sprintf("Directory entries verification discovery and the editor language scan may walk (default %d, ceiling %d)", defaults.MaxEntries, adopt.VerificationEntriesCeiling)),
		files:   fs.Int("verification-max-files", 0, fmt.Sprintf("Verification input files discovery may read (default %d, ceiling %d)", defaults.MaxFiles, adopt.VerificationFilesCeiling)),
		depth:   fs.Int("verification-max-depth", 0, fmt.Sprintf("Directory depth verification discovery may descend (default %d, ceiling %d)", defaults.MaxDepth, adopt.VerificationDepthCeiling)),
	}
}

// limits returns the parsed flags as adoption limits, nil when none was raised.
func (f verificationLimitFlags) limits() *adopt.VerificationLimits {
	return verificationLimitsFromFlags(*f.entries, *f.files, *f.depth)
}

// verificationLimitsFromFlags returns nil when no discovery bound was raised so adoption
// keeps its defaults. A raised bound leaves the others at their defaults; adoption itself
// validates every value against its ceilings, nothing is widened here.
func verificationLimitsFromFlags(entries, files, depth int) *adopt.VerificationLimits {
	if entries == 0 && files == 0 && depth == 0 {
		return nil
	}
	limits := adopt.DefaultVerificationLimits()
	if entries != 0 {
		limits.MaxEntries = entries
	}
	if files != 0 {
		limits.MaxFiles = files
	}
	if depth != 0 {
		limits.MaxDepth = depth
	}
	return &limits
}

func batchAdoptMissing(ctx context.Context, devDir string, dryRun, force, recordBaseline bool, sourceRoots ...string) error {
	if len(sourceRoots) > 1 {
		return fmt.Errorf("batch adoption accepts one lock source root")
	}
	var sourceRoot string
	if len(sourceRoots) == 1 {
		sourceRoot = sourceRoots[0]
	}
	scan, err := harvester.ScanLocalWorkstation(ctx, devDir)
	if err != nil {
		return fmt.Errorf("scanning workstation: %w", err)
	}

	fmt.Printf("=== Batch Repository Adoption (%d unmanaged repos found) ===\n", len(scan.MissingRulesRepos))
	failed := 0
	for i := 0; i < len(scan.MissingRulesRepos) && i < harvester.MaxDevScanEntries; i++ {
		repoName := scan.MissingRulesRepos[i]
		opts := adopt.AdoptOptions{
			Path:           filepath.Join(devDir, repoName),
			LockSourceRoot: sourceRoot,
			DryRun:         dryRun,
			Force:          force,
			RecordBaseline: recordBaseline,
		}
		rep, err := adopt.Adopt(ctx, opts)
		if !printBatchResult(repoName, dryRun, rep, err) {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d of %d repositories failed", errAdoptIncomplete, failed, len(scan.MissingRulesRepos))
	}
	return nil
}

// printBatchResult reports one --all-missing adoption. repoName is relative to the dev root
// and built with the host separator; it is shown in slash form on every platform.
func printBatchResult(repoName string, dryRun bool, rep *adopt.AdoptReport, err error) bool {
	repoName = util.NormalizeSlashes(repoName)
	if err != nil {
		fmt.Printf("[FAIL] %s: %v\n", repoName, err)
		if rep != nil {
			fmt.Printf("  Written before failure: %d created, %d reconciled, %d replaced\n",
				len(rep.CreatedFiles), len(rep.ReconciledNotReplaced()), len(rep.Replaced()))
		}
		return false
	}
	fmt.Printf("\n[ADOPTED] %s (State: %s, Archetype: %s, DryRun: %v)\n", repoName, rep.State, rep.Archetype, dryRun)
	fmt.Printf("  Created:    %d files\n", len(rep.CreatedFiles))
	fmt.Printf("  Reconciled: %d files\n", len(rep.ReconciledNotReplaced()))
	fmt.Printf("  Replaced:   %d files\n", len(rep.Replaced()))
	if rep.LegacyDebtCount > 0 {
		fmt.Printf("  Legacy Debt Baselined: %d infractions\n", rep.LegacyDebtCount)
	}
	printAdoptIssues(rep)
	return len(rep.Errors) == 0
}

func findDetail(details []adopt.ActionDetail, path string) string {
	for _, d := range details {
		if d.Path == path {
			return d.Details
		}
	}
	return ""
}

func printAdoptReport(rep *adopt.AdoptReport) {
	fmt.Println("=== Praetor Universal Repository Adoption ===")
	if rep.DryRun {
		fmt.Println("[DRY-RUN SIMULATION: No filesystem mutations performed]")
	}
	fmt.Printf("Target State:       %s\n", rep.State)
	fmt.Printf("Archetype:          %s\n", rep.Archetype)
	fmt.Printf("Facets:             %v\n", rep.Facets)
	for _, note := range rep.FacetNotes {
		fmt.Printf("  %s\n", note)
	}

	printDebtSummary(rep)
	printAdoptedFiles(rep)
	printAdoptPreviews(rep.Previews)
	printAdoptIssues(rep)

	// Each pillar line is derived from the step that owns it (adopt.AdoptReport.Pillars), so a
	// warned, failed, declined or unreached step never prints a success mark (BUG-871).
	outcome := rep.Outcome()
	fmt.Println("\n--- Governance Pillars " + pillarHeadings[outcome] + " ---")
	for _, pillar := range rep.Pillars() {
		fmt.Println("  " + pillar.Line())
	}

	switch outcome {
	case adopt.OutcomeIncomplete:
		fmt.Printf("\nAdoption finished with %d error(s); the repository is not fully governed yet.\n", len(rep.Errors))
	case adopt.OutcomeSimulated:
		fmt.Println("\nSimulated adoption plan completed. Run without -dry-run to apply.")
	default:
		printAppliedOutcome(rep)
	}
}

// printAppliedOutcome closes an applied run. A Verification Gate left short of ready, such as one
// whose verify-all can only exit 1, qualifies the line, so the run never ends with an unqualified
// success its verify-all contradicts (#594). An informational warning on another pillar, such as a
// preserved DevContainer, stays on that pillar's line and leaves the success line as it is
// (PendingPillars).
func printAppliedOutcome(rep *adopt.AdoptReport) {
	if pending := rep.PendingPillars(); len(pending) > 0 {
		fmt.Printf("\nRepository adopted into cordanaLLM/praetor governance; not ready yet: %s. See the warnings above.\n",
			strings.Join(pending, ", "))
		return
	}
	fmt.Println("\nRepository successfully adopted into cordanaLLM/praetor governance!")
}

var pillarHeadings = map[adopt.AdoptOutcome]string{
	adopt.OutcomeIncomplete: "Not Synchronized (incomplete adoption)",
	adopt.OutcomeSimulated:  "Planned (dry-run; not written)",
	adopt.OutcomeApplied:    "Synchronized",
}

// printDebtSummary prints the baselined legacy debt breakdown.
func printDebtSummary(rep *adopt.AdoptReport) {
	switch rep.BaselineStatus {
	case "scanned":
		printScannedDebt(rep)
	case "existing":
		printExistingDebt(rep)
	case "skipped":
		fmt.Println("\n--- Legacy Technical Debt: scan skipped (baseline recording disabled) ---")
	default:
		status := rep.BaselineStatus
		if status == "" {
			status = "not_run"
		}
		fmt.Printf("\n--- Legacy Technical Debt: baseline %s; no usable baseline result ---\n", status)
	}
}

func printScannedDebt(rep *adopt.AdoptReport) {
	label := "Baselined"
	if rep.DryRun {
		label = "Scanned (dry-run; not written)"
	}
	if rep.LegacyDebtCount == 0 {
		fmt.Printf("\n--- Legacy Technical Debt: 0 infractions (%s) ---\n", label)
		return
	}
	fmt.Printf("\n--- Legacy Technical Debt %s (%d infractions) ---\n", label, rep.LegacyDebtCount)
	printDebtBreakdown(rep, !rep.DryRun)
}

func printExistingDebt(rep *adopt.AdoptReport) {
	fmt.Printf("\n--- Existing Legacy Technical Debt Baseline: %d infractions ---\n", rep.LegacyDebtCount)
}

func printDebtBreakdown(rep *adopt.AdoptReport, explain bool) {
	var ruleKeys []string
	for r := range rep.DebtBreakdown {
		ruleKeys = append(ruleKeys, r)
	}
	sort.Strings(ruleKeys)
	for _, r := range ruleKeys {
		fmt.Printf("  • %-8s: %d infractions\n", r, rep.DebtBreakdown[r])
	}
	if explain {
		fmt.Println("  (Infractions recorded into .standards-baseline.json to prevent CI breaks while ratcheting)")
	}
}

// printAdoptIssues prints the warnings and errors an adoption run recorded.
func printAdoptIssues(rep *adopt.AdoptReport) {
	if len(rep.Warnings) > 0 {
		fmt.Printf("\nWarnings (%d):\n", len(rep.Warnings))
		for _, w := range rep.Warnings {
			fmt.Printf("  ! %s\n", w)
		}
	}
	if len(rep.Errors) > 0 {
		fmt.Printf("\nErrors (%d):\n", len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Printf("  x %s\n", e)
		}
	}
}

// fileSections names the file sections of an adoption report and the tag of each entry, for a
// run that wrote (appliedFileSections) or a dry run that only plans (plannedFileSections).
type fileSections struct {
	created, createdTag       string
	reconciled, reconciledTag string
	replaced, replacedTag     string
}

var (
	appliedFileSections = fileSections{
		created: "Files Created", createdTag: "+ [NEW] ",
		reconciled: "Files Reconciled", reconciledTag: "~ [SYNC]",
		replaced: "Files Replaced", replacedTag: "! [REPL]",
	}
	plannedFileSections = fileSections{
		created: "Files Planned for Creation", createdTag: "+ [PLAN] ",
		reconciled: "Files Planned for Reconciliation", reconciledTag: "~ [PLAN] ",
		replaced: "Files Planned for Replacement", replacedTag: "! [PLAN] ",
	}
)

// printAdoptedFiles prints the created, reconciled and replaced files. A replaced file, whose
// adopter bytes were overwritten, gets its own section with its replace entry (line delta and
// backup), and is not repeated among the reconciled files.
func printAdoptedFiles(rep *adopt.AdoptReport) {
	sections := appliedFileSections
	if rep.DryRun {
		sections = plannedFileSections
	}
	if len(rep.CreatedFiles) > 0 {
		fmt.Printf("\n%s (%d):\n", sections.created, len(rep.CreatedFiles))
		for _, f := range rep.CreatedFiles {
			printAdoptedFile(sections.createdTag, f, findDetail(rep.ActionDetails, f))
		}
	}
	replaced := rep.Replaced()
	reconciled := rep.ReconciledNotReplaced()
	if len(reconciled) > 0 {
		fmt.Printf("\n%s (%d):\n", sections.reconciled, len(reconciled))
		for _, f := range reconciled {
			printAdoptedFile(sections.reconciledTag, f, findDetail(rep.ActionDetails, f))
		}
	}
	if len(replaced) > 0 {
		fmt.Printf("\n%s (%d):\n", sections.replaced, len(replaced))
		for _, entry := range replaced {
			printAdoptedFile(sections.replacedTag, entry.Path, entry.Details)
		}
	}
}

// printAdoptPreviews prints what a dry run found each previewed file would come to
// (adopt.FilePreview.Text, the text the MCP adopt tool prints too). A real run records no
// previews and prints nothing here.
func printAdoptPreviews(previews []adopt.FilePreview) {
	for _, p := range previews {
		fmt.Print(p.Text())
	}
}

func printAdoptedFile(prefix, file, detail string) {
	if detail != "" {
		fmt.Printf("  %s %-36s : %s\n", prefix, file, detail)
		return
	}
	fmt.Printf("  %s %s\n", prefix, file)
}
