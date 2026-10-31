package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
)

// baselineScanTimeout bounds the repository walk performed by --record and --verify (HISS-02).
const baselineScanTimeout = 5 * time.Minute

// errBaselineMissing is the distinct verdict of --verify when there is no baseline file: with
// nothing recorded there is no ratchet to check, which is neither a pass nor new debt.
var errBaselineMissing = errors.New("no baseline file to verify")

// baselineMode is the parsed baseline invocation.
type baselineMode struct {
	path               string
	record             bool
	recordImageRefs    bool
	verify             bool
	allowIncrease      bool
	reason             string
	allowGosecIncrease bool
	gosecReason        string
	allViolations      bool
}

func runBaseline(args []string) error {
	mode, err := parseBaselineMode(args)
	if err != nil {
		return err
	}
	b, err := baseline.LoadClassBaseline(mode.path, baseline.ClassForFile(mode.path))
	if err != nil {
		return fmt.Errorf("failed to load baseline: %w", err)
	}

	switch {
	case mode.recordImageRefs:
		return recordImageRefBaselineCmd(mode.path, mode.allowIncrease, mode.reason)
	case mode.record:
		hissOpts := baseline.RecordOptions{AllowIncrease: mode.allowIncrease, Rationale: mode.reason}
		gosecOpts := baseline.RecordOptions{AllowIncrease: mode.allowGosecIncrease, Rationale: mode.gosecReason}
		return recordBaseline(mode.path, hissOpts, gosecOpts)
	case mode.verify:
		return verifyBaseline(mode.path, b, mode.allViolations)
	}
	return printBaseline(mode.path, b)
}

func parseBaselineMode(args []string) (baselineMode, error) {
	var mode baselineMode
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.StringVar(&mode.path, "file", ".standards-baseline.json", "Path to baseline file; its directory is the scanned root")
	fs.BoolVar(&mode.record, "record", false, "Record current infractions into baseline file")
	fs.BoolVar(&mode.recordImageRefs, "record-image-refs", false, "Record current unexcused HISS-11 container image reference findings into baseline file")
	fs.BoolVar(&mode.verify, "verify", false,
		"Rescan read-only and fail when the repository carries an infraction the baseline does not record or more than it records; never writes the file")
	fs.BoolVar(&mode.allowIncrease, "allow-increase", false, "Permit --record to raise the infraction count (HISS-13 exception); requires --reason")
	fs.StringVar(&mode.reason, "reason", "", "Rationale stored in the baseline when --allow-increase raises the count")
	fs.BoolVar(&mode.allowGosecIncrease, "allow-gosec-increase", false, "Deprecated: gosec increases are refused; retained for argument compatibility")
	fs.StringVar(&mode.gosecReason, "gosec-reason", "", "Deprecated gosec increase rationale; retained for argument compatibility")
	fs.BoolVar(&mode.allViolations, "all-violations", false,
		"With --verify, list every violation of a rejection instead of the first three per class")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return mode, err
	}
	if len(positional) > 0 {
		return mode, fmt.Errorf("baseline accepts no positional arguments, got %q", positional)
	}
	if mode.verify && mode.writesRequested() {
		return mode, errors.New("--verify is read-only and cannot be combined with --record, --allow-increase, --reason, --allow-gosec-increase or --gosec-reason")
	}
	if mode.allViolations && !mode.verify {
		return mode, errors.New("--all-violations lists a --verify rejection; pass it together with --verify")
	}
	return mode, nil
}

func (mode baselineMode) writesRequested() bool {
	return mode.record || mode.recordImageRefs || mode.allowIncrease || mode.reason != "" || mode.allowGosecIncrease || mode.gosecReason != ""
}

func recordImageRefBaselineCmd(path string, allowIncrease bool, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()
	root := filepath.Dir(path)
	repo, commit, err := baselineIdentity(ctx, root)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	var exceptions []config.Exception
	if manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml")); err == nil && manifest != nil {
		exceptions = manifest.Exceptions
	}
	res, err := supplychain.RecordImageRefBaseline(ctx, supplychain.ImageBaselineRecordOptions{
		Root:          root,
		BaselinePath:  path,
		AllowIncrease: allowIncrease,
		Reason:        reason,
		Repository:    repo,
		CommitSHA:     commit,
		Exceptions:    exceptions,
		Today:         time.Now(),
	})
	if err != nil {
		return err
	}
	return saveFileBaseline(res.Path, res.Previous, res.Next)
}

// scanBaselineInfractions scans root under the complexity policy `praetorctl audit` enforces
// there (config.ResolveRepositoryComplexity resolves a locked repository exactly as the audit
// does) and keys the violations (hiss.ConvertToBaseline). --record used to scan with the scanner defaults
// instead, so in a repository that tightened its function-length limit the recorded baseline
// and the audit that judges it disagreed on what counts as debt. It also returns the scan
// options, so --verify attributes a rejection under the same policy.
func scanBaselineInfractions(ctx context.Context, root string) ([]baseline.Infraction, hiss.ScanOptions, error) {
	scanOpts, warning, err := config.ResolveRepositoryScanOptions(ctx, root, hiss.ScanOptions{})
	if err != nil {
		return nil, scanOpts, fmt.Errorf("resolve complexity policy for %s: %w", root, err)
	}
	if warning != "" {
		fmt.Printf("[WARN] %s\n", warning)
	}
	scanRep, err := hiss.Scan(ctx, root, scanOpts)
	if err != nil {
		return nil, scanOpts, fmt.Errorf("failed to scan for baseline infractions: %w", err)
	}
	if scanRep.Truncated {
		return nil, scanOpts, fmt.Errorf("refusing an incomplete scan: %w", hiss.ErrScanTruncated)
	}
	return hiss.ConvertToBaseline(scanRep.Violations), scanOpts, nil
}

// verifyBaseline is the read-only ratchet check: it rescans and evaluates the stored baseline
// with baseline.EvaluateRatchet, the rule the audit applies, and never writes the file. The
// touched-file clean rule needs a change set, which is the audit's input (`praetorctl audit
// --base=<ref>`); this check has none, so it applies the count and new-fingerprint rules only.
// A rejection attributes each unbaselined finding and, with all, lists every one of them
// (describeRejection): the read-only way to see what fails without rewriting the file (#598).
func verifyBaseline(path string, b *baseline.Baseline, all bool) error {
	if b.Absent {
		return fmt.Errorf("[FAIL] %w at %s; record the current debt with 'praetorctl baseline --record'", errBaselineMissing, path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()
	root := filepath.Dir(path)
	current, scanOpts, err := scanBaselineInput(ctx, root, path, b)
	if err != nil {
		return fmt.Errorf("[FAIL] verify %s: %w", path, err)
	}
	if err := verifyFileBaseline(ctx, root, path, b, current, scanOpts, all); err != nil {
		return err
	}
	if auxiliaryBaselineFile(path) {
		return nil
	}
	return verifyOptionalBaselines(ctx, root, scanOpts, all)
}

// auxiliaryBaselineFile reports whether path names one of the per-class snapshots beside the HISS baseline
// (.readme-baseline.json, .gosec-baseline.json); a command given one of them handles that file alone.
func auxiliaryBaselineFile(path string) bool {
	base := filepath.Base(path)
	return base == gating.ReadmeBaselineFile || base == gating.GosecBaselineFile
}

func verifyOptionalBaselines(ctx context.Context, root string, scanOpts hiss.ScanOptions, all bool) error {
	for _, file := range []string{gating.GosecBaselineFile, gating.ReadmeBaselineFile} {
		class := baseline.ClassForFile(file)
		full := filepath.Join(root, file)
		previous, err := baseline.LoadClassBaseline(full, class)
		if err != nil {
			return err
		}
		skip, err := skipOptionalBaseline(root, file, previous)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		findings, err := scanFileBaseline(ctx, root, file, previous)
		if err != nil {
			return err
		}
		if err := verifyFileBaseline(ctx, root, full, previous, findings, scanOpts, all); err != nil {
			return err
		}
	}
	return nil
}

func skipOptionalBaseline(root, file string, previous *baseline.Baseline) (bool, error) {
	if !previous.Absent {
		return false, nil
	}
	if file == gating.GosecBaselineFile {
		return true, nil
	}
	enforce, err := config.ReadmePresentationEnforced(root)
	if err != nil {
		return false, err
	}
	if !enforce {
		fmt.Println("[INFO] " + readmepresentation.NotRecorded)
	}
	return !enforce, nil
}

func verifyFileBaseline(ctx context.Context, root, path string, b *baseline.Baseline, current []baseline.Infraction, scanOpts hiss.ScanOptions, all bool) error {
	ratchet := baseline.EvaluateRatchet(b, current, nil)
	if baseline.ClassForFile(path) == baseline.READMEClass {
		ratchet.Subject = "README presentation findings"
	}
	if !ratchet.Passed {
		detail := ratchet.Summary()
		if baseline.ClassForFile(path) != baseline.READMEClass {
			detail = describeRejection(ctx, root, path, scanOpts, b, current, ratchet, all)
		} else if all {
			detail = ratchet.FullSummary()
		}
		return fmt.Errorf("[FAIL] %s: %s", path, detail)
	}
	if b.Absent && baseline.ClassForFile(path) == baseline.READMEClass {
		fmt.Println("[INFO] " + readmepresentation.NotRecordedClean)
		return nil
	}
	fmt.Printf("[PASS] HISS-13 debt ratchet: %d active infractions within the %d recorded in %s; the file was not rewritten.\n", ratchet.CurrentCount, b.Count(), path)
	if notice := ratchet.StaleNotice(); notice != "" {
		fmt.Printf("[WARN] %s (%s)\n", notice, path)
	}
	return nil
}

// baselineIdentity names the repository and commit a baseline recorded in dir belongs to:
// the origin remote's owner/name (util.ResolveRemoteIdentity, the source adoption records
// too) and the HEAD commit, each "" when the checkout has none. A Git read that was not
// answered is an error rather than an empty identity.
func baselineIdentity(ctx context.Context, dir string) (repository, commit string, err error) {
	owner, name, err := util.ResolveRemoteIdentity(ctx, dir)
	switch {
	case err == nil:
		repository = owner + "/" + name
	case !errors.Is(err, util.ErrRepoIdentityUnresolved):
		return "", "", fmt.Errorf("resolve repository identity: %w", err)
	}
	commit, err = state.RecordedCommit(ctx, dir)
	if err != nil {
		return "", "", err
	}
	return repository, commit, nil
}

// recordBaseline rescans the repository around the baseline and replaces the snapshot,
// refusing to raise the count unless the operator allowed it with a rationale.
func recordBaseline(path string, hissOpts, gosecOpts baseline.RecordOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()
	root := filepath.Dir(path)
	repo, commit, err := baselineIdentity(ctx, root)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	hissOpts.Repository, hissOpts.CommitSHA = repo, commit
	gosecOpts.Repository, gosecOpts.CommitSHA = repo, commit
	paths := []string{path}
	if filepath.Base(path) != gating.ReadmeBaselineFile && filepath.Base(path) != gating.GosecBaselineFile {
		paths = append(paths, filepath.Join(root, gating.GosecBaselineFile), filepath.Join(root, gating.ReadmeBaselineFile))
	}
	plans := make([]baselineRecordPlan, 0, len(paths))
	for i := 0; i < len(paths); i++ {
		opts := hissOpts
		if filepath.Base(paths[i]) == gating.GosecBaselineFile {
			opts = gosecOpts
		}
		plan, err := planFileBaseline(ctx, root, paths[i], opts)
		if err != nil {
			return err
		}
		if plan.next != nil {
			plans = append(plans, plan)
		}
	}
	for i := 0; i < len(plans); i++ {
		plan := plans[i]
		if err := saveFileBaseline(plan.path, plan.previous, plan.next); err != nil {
			return err
		}
	}
	return nil
}

type baselineRecordPlan struct {
	path           string
	previous, next *baseline.Baseline
}

// planFileBaseline validates a rescan without changing any recorded snapshot.
func planFileBaseline(ctx context.Context, root, path string, opts baseline.RecordOptions) (baselineRecordPlan, error) {
	plan := baselineRecordPlan{path: path}
	previous, err := baseline.LoadClassBaseline(path, baseline.ClassForFile(path))
	if err != nil {
		return plan, err
	}
	findings, err := scanFileBaseline(ctx, root, filepath.Base(path), previous)
	if err != nil {
		return plan, err
	}
	if previous.Absent && len(findings) == 0 && auxiliaryBaselineFile(path) {
		return plan, nil
	}
	next, err := recordFileSnapshot(ctx, root, path, previous, findings, opts)
	if err != nil {
		return plan, formatBaselineRecordError(path, err)
	}
	plan.previous, plan.next = previous, next
	return plan, nil
}

func recordFileSnapshot(
	ctx context.Context,
	root, path string,
	previous *baseline.Baseline,
	findings []baseline.Infraction,
	opts baseline.RecordOptions,
) (*baseline.Baseline, error) {
	if filepath.Base(path) == gating.GosecBaselineFile {
		return guardedGosecRecord(ctx, root, previous, findings, opts)
	}
	next, err := baseline.Record(previous, findings, opts)
	if err == nil && !auxiliaryBaselineFile(path) {
		err = verifyCarriedReleaseTestLevels(ctx, root, filepath.Base(path), previous, next)
	}
	return next, err
}

func formatBaselineRecordError(path string, err error) error {
	if filepath.Base(path) == gating.GosecBaselineFile || errors.Is(err, baseline.ErrReleaseTestLevelShrinkOnly) {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	return fmt.Errorf("refusing to record %s: %w (pass --allow-increase --reason=<why> to record a deliberate increase)", path, err)
}

func verifyCarriedReleaseTestLevels(ctx context.Context, root, file string, previous, next *baseline.Baseline) error {
	defBase, err := defaultBranchBaseline(ctx, root, file, previous)
	if err != nil {
		return err
	}
	return baseline.CheckReleaseTestLevelsSubset(defBase, next)
}

// scanBaselineInput preserves the HISS attribution policy for HISS snapshots.
func scanBaselineInput(ctx context.Context, root, path string, previous *baseline.Baseline) ([]baseline.Infraction, hiss.ScanOptions, error) {
	if filepath.Base(path) != gating.ReadmeBaselineFile && filepath.Base(path) != gating.GosecBaselineFile {
		return scanBaselineInfractions(ctx, root)
	}
	findings, err := scanFileBaseline(ctx, root, filepath.Base(path), previous)
	return findings, hiss.ScanOptions{}, err
}

func scanFileBaseline(ctx context.Context, root, file string, previous *baseline.Baseline) ([]baseline.Infraction, error) {
	if file == gating.GosecBaselineFile {
		findings, _, err := gating.ScanGosec(ctx, root)
		return findings, err
	}
	if file != gating.ReadmeBaselineFile {
		findings, _, err := scanBaselineInfractions(ctx, root)
		return findings, err
	}
	enforce, err := config.ReadmePresentationEnforced(root)
	if err != nil {
		return nil, err
	}
	findings, err := readmepresentation.ScanBaselineFindings(ctx, root, previous, enforce)
	return readmepresentation.ToBaseline(findings), err
}

func saveFileBaseline(path string, previous, next *baseline.Baseline) error {
	label := "Baseline"
	if filepath.Base(path) == gating.GosecBaselineFile {
		label = "Gosec baseline"
	}
	if !previous.Absent && previous.SameDebt(next) && previous.CommitSHA == next.CommitSHA {
		fmt.Printf("%s unchanged: %s (Total: %d infractions, previously %d); kept the recorded file\n", label, path, next.Count(), previous.Count())
		return nil
	}
	if err := baseline.SaveBaseline(path, next); err != nil {
		return err
	}
	fmt.Printf("%s successfully updated: %s (Total: %d infractions, previously %d)\n", label, path, next.Count(), previous.Count())
	if next.IncreaseRationale != "" {
		fmt.Printf("[WARN] Debt increased deliberately in %s; recorded rationale: %s\n", path, next.IncreaseRationale)
	}
	return nil
}

// printBaseline lists the recorded infractions. Inspect mode reads the stored snapshot and
// never scans, so it states what was recorded, never whether the repository complies: a
// missing file and a recorded zero both used to print "100% compliant" (BUG-802).
func printBaseline(path string, b *baseline.Baseline) error {
	if b.Absent {
		fmt.Printf("No baseline file at %s: no technical debt has been recorded and nothing was scanned.\n", path)
		fmt.Println("Run 'praetorctl baseline --record' to scan and record, or 'praetorctl audit' for a live check.")
		return nil
	}
	repo := b.Repository
	if repo == "" {
		repo = "repository"
	}
	fmt.Printf("=== %s Technical Debt Baseline ===\n", repo)
	fmt.Printf("File: %s | Total Infractions: %d\n", path, b.Count())
	if b.IncreaseRationale != "" {
		fmt.Printf("Recorded increase rationale: %s\n", b.IncreaseRationale)
	}
	for i, inf := range b.Infractions {
		fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Symbol, inf.Message)
	}

	if b.Count() == 0 {
		fmt.Println("Zero technical debt recorded in this baseline.")
	}
	if !auxiliaryBaselineFile(path) {
		if err := printOptionalBaselines(filepath.Dir(path), repo); err != nil {
			return err
		}
	}
	fmt.Println("This is the stored snapshot, not a live scan; run 'praetorctl baseline --verify' or 'praetorctl audit' to check the repository.")
	return nil
}

func printOptionalBaselines(repoDir, repo string) error {
	for _, file := range []string{gating.GosecBaselineFile, gating.ReadmeBaselineFile} {
		class := baseline.HISSClass
		if file == gating.ReadmeBaselineFile {
			class = baseline.READMEClass
		}
		path := filepath.Join(repoDir, file)
		base, err := baseline.LoadClassBaseline(path, class)
		if err != nil {
			return err
		}
		if base.Absent {
			continue
		}
		fmt.Printf("\n=== %s %s Technical Debt Baseline ===\n", repo, file)
		fmt.Printf("File: %s | Total Infractions: %d\n", path, base.Count())
		if base.IncreaseRationale != "" {
			fmt.Printf("Recorded increase rationale: %s\n", base.IncreaseRationale)
		}
		for i, inf := range base.Infractions {
			fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Anchor, inf.Message)
		}
		if base.Count() == 0 {
			fmt.Printf("Zero technical debt recorded in %s.\n", file)
		}
	}
	return nil
}
