package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

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
	if page.Source.Path != source.SourcePath || page.Complete != (index == len(pass.Pages)-1) {
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
	for i := 0; i < len(repo.Attempts) && i < MaxPublicAttempts; i++ {
		if err := validateRepairAttempt(repo.Attempts[i], i+1); err != nil {
			return err
		}
	}
	count := len(repo.Attempts)
	if repo.Attempts[count-1].TreeDigest != repo.Attempts[count-2].TreeDigest {
		return errors.New("public verification did not stabilize")
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

func validateRepairAttempt(attempt PublicAttempt, number int) error {
	if attempt.Number != number || attempt.Error != "" || !suiteSHA.MatchString(attempt.TreeDigest) {
		return errors.New("public attempt identity mismatch")
	}
	return validateRepairVerification(attempt.Verification)
}

func validateRepairVerification(check *PublicVerification) error {
	if check == nil || !check.LockVerified || !check.ContextVerified || check.Scan == nil || check.Scan.Truncated || check.Ratchet == nil || !check.Ratchet.Passed {
		return errors.New("public verification attempt is incomplete")
	}
	return nil
}
