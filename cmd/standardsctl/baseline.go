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
	b, err := baseline.LoadBaseline(mode.path)
	if err != nil {
		return fmt.Errorf("failed to load baseline: %w", err)
	}

	switch {
	case mode.record:
		hissOpts := baseline.RecordOptions{AllowIncrease: mode.allowIncrease, Rationale: mode.reason}
		gosecOpts := baseline.RecordOptions{AllowIncrease: mode.allowGosecIncrease, Rationale: mode.gosecReason}
		return recordBaseline(mode.path, b, hissOpts, gosecOpts)
	case mode.verify:
		return verifyBaseline(mode.path, b, mode.allViolations)
	}
	printBaseline(mode.path, b)
	return nil
}

func parseBaselineMode(args []string) (baselineMode, error) {
	var mode baselineMode
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	fs.StringVar(&mode.path, "file", ".standards-baseline.json", "Path to baseline file; its directory is the scanned root")
	fs.BoolVar(&mode.record, "record", false, "Record current infractions into baseline file")
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
	return mode.record || mode.allowIncrease || mode.reason != "" || mode.allowGosecIncrease || mode.gosecReason != ""
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
	current, scanOpts, err := scanBaselineInfractions(ctx, root)
	if err != nil {
		return fmt.Errorf("[FAIL] verify %s: %w", path, err)
	}
	enforce, err := config.ReadmePresentationEnforced(root)
	if err != nil {
		return fmt.Errorf("[FAIL] verify %s: %w", path, err)
	}
	readmeFindings, err := readmepresentation.ScanBaselineFindings(ctx, root, b, enforce)
	if err != nil {
		return fmt.Errorf("[FAIL] verify %s: %w", path, err)
	}
	current = append(current, readmepresentation.ToBaseline(readmeFindings)...)
	ratchet := baseline.EvaluateRatchet(b, current, nil)
	if !ratchet.Passed {
		return fmt.Errorf("[FAIL] %s: %s", path, describeRejection(ctx, root, path, scanOpts, b, current, ratchet, all))
	}
	fmt.Printf("[PASS] HISS-13 debt ratchet: %d active infractions within the %d recorded in %s; the file was not rewritten.\n",
		ratchet.CurrentCount, b.TotalInfractions, path)
	if notice := ratchet.StaleNotice(); notice != "" {
		// Read-only, like the rest of --verify: the audit's --max-stale-baseline-entries is the
		// bound that fails a run (#349).
		fmt.Printf("[WARN] %s\n", notice)
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
func recordBaseline(path string, previous *baseline.Baseline, hissOpts, gosecOpts baseline.RecordOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()

	root := filepath.Dir(path)
	current, _, err := scanBaselineInfractions(ctx, root)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	enforce, err := config.ReadmePresentationEnforced(root)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	readmeFindings, err := readmepresentation.ScanBaselineFindings(ctx, root, previous, enforce)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}

	repo, commit, err := baselineIdentity(ctx, root)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", path, err)
	}
	hissOpts.Repository, hissOpts.CommitSHA = repo, commit
	gosecOpts.Repository, gosecOpts.CommitSHA = repo, commit

	next, err := readmepresentation.RecordBaseline(previous, readmeFindings, hissOpts, current...)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w (pass --allow-increase --reason=<why> to record a deliberate increase)", path, err)
	}
	// A rescan that finds the same debt keeps the recorded file byte for byte, as adoption's
	// explicit re-record does (rerecordBaseline in internal/adopt/adopt.go, under this same
	// condition): rewriting it only moved generated_at and dropped an earlier increase
	// rationale, so the baseline, a generated artefact (ADR-0017), never rendered fresh. A
	// baseline that records no commit yet is still written, so it gains one (BUG-801).
	if !previous.Absent && previous.SameDebt(next) && previous.CommitSHA == next.CommitSHA {
		fmt.Printf("Baseline unchanged: %s (Total: %d infractions, previously %d); kept the recorded file\n",
			path, next.TotalInfractions, previous.Count())
	} else {
		if err := baseline.SaveBaseline(path, next); err != nil {
			return fmt.Errorf("failed to save baseline: %w", err)
		}
		fmt.Printf("Baseline successfully updated: %s (Total: %d infractions, previously %d)\n", path, next.TotalInfractions, previous.Count())
		if next.IncreaseRationale != "" {
			fmt.Printf("[WARN] Debt increased deliberately; recorded rationale: %s\n", next.IncreaseRationale)
		}
	}
	return recordGosecBaseline(ctx, filepath.Dir(path), gosecOpts)
}

func recordGosecBaseline(ctx context.Context, repoDir string, opts baseline.RecordOptions) error {
	gosecFindings, hasGo, err := gating.ScanGosec(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("scan gosec infractions: %w", err)
	}
	if !hasGo {
		return nil
	}
	gosecPath := filepath.Join(repoDir, gating.GosecBaselineFile)
	prevGosec, err := baseline.LoadBaseline(gosecPath)
	if err != nil {
		return fmt.Errorf("load gosec baseline: %w", err)
	}
	if prevGosec.Absent && len(gosecFindings) == 0 && !opts.AllowIncrease {
		return nil
	}
	nextGosec, err := guardedGosecRecord(ctx, repoDir, prevGosec, gosecFindings, opts)
	if err != nil {
		return fmt.Errorf("refusing to record %s: %w", gosecPath, err)
	}
	return saveGosecBaseline(gosecPath, prevGosec, nextGosec)
}

func saveGosecBaseline(gosecPath string, prev, next *baseline.Baseline) error {
	if !prev.Absent && prev.SameDebt(next) && prev.CommitSHA == next.CommitSHA {
		fmt.Printf("Gosec baseline unchanged: %s (Total: %d infractions, previously %d); kept the recorded file\n",
			gosecPath, next.TotalInfractions, prev.Count())
		return nil
	}
	if err := baseline.SaveBaseline(gosecPath, next); err != nil {
		return fmt.Errorf("failed to save gosec baseline: %w", err)
	}
	fmt.Printf("Gosec baseline successfully updated: %s (Total: %d infractions, previously %d)\n",
		gosecPath, next.TotalInfractions, prev.Count())
	if next.IncreaseRationale != "" {
		fmt.Printf("[WARN] Gosec debt increased deliberately; recorded rationale: %s\n", next.IncreaseRationale)
	}
	return nil
}

// printBaseline lists the recorded infractions. Inspect mode reads the stored snapshot and
// never scans, so it states what was recorded, never whether the repository complies: a
// missing file and a recorded zero both used to print "100% compliant" (BUG-802).
func printBaseline(path string, b *baseline.Baseline) {
	if b.Absent {
		fmt.Printf("No baseline file at %s: no technical debt has been recorded and nothing was scanned.\n", path)
		fmt.Println("Run 'praetorctl baseline --record' to scan and record, or 'praetorctl audit' for a live check.")
		return
	}
	repo := b.Repository
	if repo == "" {
		repo = "repository"
	}
	fmt.Printf("=== %s Technical Debt Baseline ===\n", repo)
	fmt.Printf("File: %s | Total Infractions: %d\n", path, b.TotalInfractions)
	if b.IncreaseRationale != "" {
		fmt.Printf("Recorded increase rationale: %s\n", b.IncreaseRationale)
	}
	for i, inf := range b.Infractions {
		fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Symbol, inf.Message)
	}

	if b.TotalInfractions == 0 {
		fmt.Println("Zero technical debt recorded in this baseline.")
	}
	printGosecBaselineIfPresent(filepath.Dir(path), repo)
	fmt.Println("This is the stored snapshot, not a live scan; run 'praetorctl baseline --verify' or 'praetorctl audit' to check the repository.")
}

func printGosecBaselineIfPresent(repoDir, repo string) {
	gosecPath := filepath.Join(repoDir, gating.GosecBaselineFile)
	gosecBase, err := baseline.LoadBaseline(gosecPath)
	if err != nil || gosecBase.Absent {
		return
	}
	fmt.Printf("\n=== %s Gosec Technical Debt Baseline ===\n", repo)
	fmt.Printf("File: %s | Total Infractions: %d\n", gosecPath, gosecBase.TotalInfractions)
	if gosecBase.IncreaseRationale != "" {
		fmt.Printf("Recorded increase rationale: %s\n", gosecBase.IncreaseRationale)
	}
	for i, inf := range gosecBase.Infractions {
		fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Anchor, inf.Message)
	}
	if gosecBase.TotalInfractions == 0 {
		fmt.Println("Zero gosec technical debt recorded in this baseline.")
	}
}
