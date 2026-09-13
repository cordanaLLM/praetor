package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

var errRepairPublicRerun = errors.New("verified public report lacks current policy or baseline evidence; rerun required")

func validateRepairReport(ctx context.Context, report *SuiteReport) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateRepairHeader(report); err != nil {
		return "", err
	}
	data, err := json.Marshal(report)
	if err != nil || len(data) > maxRepairReportBytes {
		return "", errors.New("repair report cannot encode within its 8 MiB bound")
	}
	if err := validateRepairJSON(data); err != nil {
		return "", err
	}
	if err := validateRepairCases(report); err != nil {
		return "", err
	}
	return repairBytesHash(data), nil
}

func validateRepairHeader(report *SuiteReport) error {
	if report == nil || report.Version != 1 || len(report.Cases) < 1 || len(report.Cases) > MaxSuiteCases {
		return errors.New("repair report requires version 1 and 1..8 cases")
	}
	if report.Options.Stage != "verify" || report.StartedAt.IsZero() || report.FinishedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) || !suiteSHA.MatchString(report.ConfigSHA256) {
		return errors.New("repair report requires completed verification and a config fingerprint")
	}
	return nil
}

func validateRepairCases(report *SuiteReport) error {
	failed := 0
	seen := make(map[string]bool)
	for i := 0; i < len(report.Cases) && i < MaxSuiteCases; i++ {
		result := report.Cases[i]
		if !suiteID.MatchString(result.ID) || seen[result.ID] {
			return errors.New("repair report case IDs must be valid and unique")
		}
		seen[result.ID] = true
		if err := validateRepairCase(result); err != nil {
			return fmt.Errorf("repair case %d: %w", i+1, err)
		}
		if result.Status != "verified" {
			failed++
		}
	}
	return validateRepairAggregate(report, failed)
}

func validateRepairAggregate(report *SuiteReport, failed int) error {
	if (failed == 0 && (report.Status != "verified" || !report.Verified)) || (failed > 0 && (report.Status != "failed" || report.Verified)) {
		return errors.New("repair report aggregate status contradicts case outcomes")
	}
	return nil
}

func validateRepairCase(result SuiteCase) error {
	if result.Status != "failed" && result.Status != "skipped_due_to_context" && result.Status != "verified" {
		return errors.New("case is not a completed result")
	}
	if (result.Status == "verified") != (result.Error == "") {
		return errors.New("case error contradicts status")
	}
	switch result.Kind {
	case "transcript":
		return validateRepairTranscriptCase(result)
	case "public":
		return validateRepairPublicCase(result)
	default:
		return errors.New("unknown case kind")
	}
}

func validateRepairTranscriptCase(result SuiteCase) error {
	if result.Transcript == nil || result.Repository != "" || result.Public != nil {
		return errors.New("transcript case input mismatch")
	}
	if result.Transcript.ID != result.ID {
		return errors.New("transcript ID differs from case")
	}
	if err := validateSuiteTranscript(*result.Transcript); err != nil {
		return err
	}
	if result.Status == "verified" {
		return validateRepairReplay(result)
	}
	return nil
}

func validateRepairPublicCase(result SuiteCase) error {
	if result.Transcript != nil || result.Ingestion != nil || result.Replay != nil {
		return errors.New("public case input mismatch")
	}
	sources, err := parsePublicSources([]string{result.Repository})
	if err != nil || len(sources) != 1 || sources[0].sha == "" {
		return errors.New("public case requires curated immutable source")
	}
	if result.Status == "verified" {
		return validateRepairPublic(result, sources[0])
	}
	return nil
}

func validateRepairReplay(result SuiteCase) error {
	first, last := result.Ingestion, result.Replay
	if err := validateRepairPass(*result.Transcript, first); err != nil {
		return err
	}
	if err := validateRepairPass(*result.Transcript, last); err != nil {
		return err
	}
	if first.Stored < 1 || first.AlreadyPresent != 0 || last.Stored != 0 || last.AlreadyPresent != first.Stored || last.Scanned != first.Scanned || last.Skipped != first.Skipped || first.Pages[0].Source != last.Pages[0].Source {
		return errors.New("verified transcript lacks consistent fresh ingestion and idempotent replay")
	}
	return nil
}

func validateRepairPass(source SuiteTranscript, pass *SuiteReplayPass) error {
	if pass == nil || !pass.Complete || len(pass.Pages) < 1 || len(pass.Pages) > maxSuitePages {
		return errors.New("verified transcript pass is incomplete")
	}
	accumulated := &SuiteReplayPass{Pages: pass.Pages}
	for i := 0; i < len(pass.Pages) && i < maxSuitePages; i++ {
		page := pass.Pages[i]
		if err := validateRepairPassPage(source, pass, accumulated, i); err != nil {
			return err
		}
		accumulateSuitePage(accumulated, page)
	}
	return validateRepairPassTotals(pass, accumulated)
}

func validateRepairPassPage(source SuiteTranscript, pass, accumulated *SuiteReplayPass, index int) error {
	page := pass.Pages[index]
	if page == nil || pass.Pages[0] == nil {
		return errors.New("transcript page is null")
	}
	if err := validateSuitePage(source, accumulated, page); err != nil {
		return err
	}
	if !repairSourcePathMatches(source, page.Source.Path) || page.Complete != (index == len(pass.Pages)-1) {
		return errors.New("transcript page identity or completion mismatch")
	}
	return nil
}

func validateRepairPassTotals(pass, accumulated *SuiteReplayPass) error {
	if pass.Scanned != accumulated.Scanned || pass.Stored != accumulated.Stored || pass.AlreadyPresent != accumulated.AlreadyPresent || pass.Skipped != accumulated.Skipped || pass.Scanned > harvester.MaxTranscriptRecords {
		return errors.New("transcript pass counters contradict page evidence")
	}
	return nil
}

func validateRepairPublic(result SuiteCase, source publicSource) error {
	report := result.Public
	if err := validateRepairPublicEnvelope(report, result.Repository); err != nil {
		return err
	}
	repo := report.Results[0]
	if err := validateRepairPublicIdentity(repo, source); err != nil {
		return err
	}
	anchor, err := repairPublicAnchor(repo)
	if err != nil {
		return err
	}
	for i := 0; i < len(repo.Attempts) && i < MaxPublicAttempts; i++ {
		if err := validateRepairAttempt(repo.Attempts[i], i+1, anchor); err != nil {
			return err
		}
	}
	count := len(repo.Attempts)
	if repo.Attempts[count-1].TreeDigest != repo.Attempts[count-2].TreeDigest {
		return errors.New("public verification did not stabilize")
	}
	if repo.Attempts[count-1].Verification.BaselineSHA256 != repo.Attempts[count-2].Verification.BaselineSHA256 {
		return errors.New("stable public tree has contradictory baseline digests")
	}
	return nil
}

func validateRepairPublicEnvelope(report *PublicLoopReport, repository string) error {
	if report == nil || !report.Verified || !report.Options.Apply || len(report.Results) != 1 || len(report.Options.Repositories) != 1 {
		return errors.New("verified public case lacks matching applied report")
	}
	if report.Options.Repositories[0] != repository {
		return errors.New("public input mismatch")
	}
	return nil
}

func validateRepairPublicIdentity(repo PublicRepositoryResult, source publicSource) error {
	if repo.Repository != source.url || repo.RequestedSHA != source.sha || repo.SourceSHA != source.sha || repo.Status != "verified" || repo.Error != "" || len(repo.Attempts) < 2 || len(repo.Attempts) > MaxPublicAttempts {
		return errors.New("verified public case lacks pinned stable attempts")
	}
	return nil
}

func repairPublicAnchor(repo PublicRepositoryResult) (publicPolicyAnchor, error) {
	if repo.Plan == nil || repo.Plan.EffectivePolicy == nil || repo.OriginalScan == nil || repo.OriginalTreeDigest == "" {
		return publicPolicyAnchor{}, errRepairPublicRerun
	}
	anchor := publicPolicyAnchor{Policy: repo.Plan.EffectivePolicy, Scan: repo.OriginalScan}
	if err := publicAdoptionError(repo.Plan, nil); err != nil {
		return anchor, err
	}
	if !repo.Plan.DryRun {
		return anchor, errors.New("public policy plan must be a dry-run report")
	}
	if err := validatePublicAnchor(anchor); err != nil {
		return anchor, err
	}
	if err := validateRepairPublicScan(anchor.Scan); err != nil {
		return anchor, err
	}
	return anchor, validateRepairOriginalMetadata(repo)
}

func validateRepairOriginalMetadata(repo PublicRepositoryResult) error {
	if !suiteSHA.MatchString(repo.OriginalTreeDigest) {
		return errors.New("public original tree requires a valid SHA-256 identity")
	}
	if repo.Plan.LegacyDebtCount != repo.OriginalScan.TotalInfractions {
		return errors.New("planned adoption debt count differs from the independent original scan")
	}
	return nil
}

func validateRepairAttempt(attempt PublicAttempt, number int, anchor publicPolicyAnchor) error {
	if attempt.Number != number || attempt.Error != "" || !suiteSHA.MatchString(attempt.TreeDigest) {
		return errors.New("public attempt identity mismatch")
	}
	if err := validateRepairAppliedPolicy(attempt, anchor); err != nil {
		return err
	}
	if err := validateRepairChangedFiles(attempt.ChangedFiles); err != nil {
		return err
	}
	return validateRepairVerification(attempt.Verification, anchor, attempt.ChangedFiles)
}

func validateRepairAppliedPolicy(attempt PublicAttempt, anchor publicPolicyAnchor) error {
	if attempt.Adoption == nil || attempt.Adoption.EffectivePolicy == nil {
		return errRepairPublicRerun
	}
	if err := publicAdoptionError(attempt.Adoption, nil); err != nil {
		return err
	}
	if attempt.Adoption.DryRun {
		return errors.New("public verification requires an applied adoption report")
	}
	if attempt.Adoption.LegacyDebtCount != anchor.Scan.TotalInfractions {
		return errors.New("applied adoption debt count differs from the independent original scan")
	}
	return matchPublicPolicy(anchor.Policy, attempt.Adoption.EffectivePolicy)
}

func validateRepairVerification(check *PublicVerification, anchor publicPolicyAnchor, changed []string) error {
	if check == nil || !check.LockVerified || !check.ContextVerified || check.Scan == nil || check.Scan.Truncated || check.Ratchet == nil || !check.Ratchet.Passed {
		return errors.New("public verification attempt is incomplete")
	}
	if err := validateRepairPolicyEvidence(check, anchor); err != nil {
		return err
	}
	if err := validateRepairPublicScan(check.Scan); err != nil {
		return err
	}
	return validateRepairRatchet(anchor.Scan, check, changed)
}

func validateRepairPolicyEvidence(check *PublicVerification, anchor publicPolicyAnchor) error {
	if check.PolicySHA256 == "" || check.MaxFuncLOC == 0 || check.BaselineSHA256 == "" || !check.BaselineVerified {
		return errRepairPublicRerun
	}
	if check.PolicySHA256 != anchor.Policy.SHA256 || check.MaxFuncLOC != anchor.Policy.Policy.Complexity.MaxFuncLOC {
		return errors.New("public verification policy differs from the planned policy")
	}
	if !suiteSHA.MatchString(check.BaselineSHA256) {
		return errors.New("public verification requires a valid baseline SHA-256")
	}
	return nil
}

func validateRepairPublicScan(scan *hiss.ScanReport) error {
	if err := validatePublicScan(scan); err != nil {
		return err
	}
	if err := scan.Coverage.Validate(); err != nil {
		return fmt.Errorf("public scan coverage: %w", err)
	}
	counts := make(map[string]int)
	for i := 0; i < len(scan.Violations) && i < hiss.MaxInfractionsCap; i++ {
		entry := scan.Violations[i]
		if entry.RuleID == "" || entry.LineNumber <= 0 || !repairPublicRelativePath(entry.FilePath) {
			return errors.New("public scan contains an invalid violation identity")
		}
		counts[entry.RuleID]++
	}
	if len(counts) != len(scan.Breakdown) {
		return errors.New("public scan breakdown contradicts its entries")
	}
	for rule, count := range counts {
		if scan.Breakdown[rule] != count {
			return errors.New("public scan breakdown contradicts its entries")
		}
	}
	return nil
}

func validateRepairChangedFiles(changed []string) error {
	// The union of the bounded original and final inventories can be twice as large.
	if len(changed) > 2*maxPublicTreeEntries {
		return errors.New("public changed-file evidence exceeds its inventory bound")
	}
	seen := make(map[string]bool, len(changed))
	for i := 0; i < len(changed) && i < 2*maxPublicTreeEntries; i++ {
		path := changed[i]
		if !repairPublicRelativePath(path) || seen[path] {
			return errors.New("public changed files must be unique canonical relative paths")
		}
		seen[path] = true
	}
	return nil
}

func repairPublicRelativePath(path string) bool {
	return filepath.IsLocal(path) && path != "." && filepath.ToSlash(filepath.Clean(path)) == path
}

func validateRepairRatchet(original *hiss.ScanReport, check *PublicVerification, changed []string) error {
	base := &baseline.Baseline{Version: 1, TotalInfractions: original.TotalInfractions, Infractions: publicInfractions(original)}
	expected := baseline.EvaluateRatchet(base, publicInfractions(check.Scan), changed)
	actual := check.Ratchet
	if !expected.Passed || actual.PreviousCount != expected.PreviousCount || actual.CurrentCount != expected.CurrentCount ||
		actual.Passed != expected.Passed || !slices.Equal(actual.NewViolations, expected.NewViolations) ||
		!slices.Equal(actual.TouchedCleanViolations, expected.TouchedCleanViolations) {
		return errors.New("public ratchet contradicts original scan, verified scan or changed-file evidence")
	}
	return nil
}

// Antigravity deliberately prefers the same-directory full counterpart. This is
// a declaration/evidence comparison only; it never probes either source path.
func repairSourcePathMatches(source SuiteTranscript, actual string) bool {
	if actual == source.SourcePath {
		return true
	}
	if source.Format != "antigravity-jsonl-v1" || filepath.Base(source.SourcePath) != "transcript.jsonl" {
		return false
	}
	return actual == filepath.Join(filepath.Dir(source.SourcePath), "transcript_full.jsonl")
}
