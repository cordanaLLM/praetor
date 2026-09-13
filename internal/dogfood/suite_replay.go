package dogfood

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/harvester"
	"github.com/cordanaLLM/praetor/internal/util"
)

const maxSuitePages = (harvester.MaxTranscriptRecords + harvester.MaxTranscriptBatchRecords - 1) / harvester.MaxTranscriptBatchRecords

// SuiteReplayPass preserves every actual page, including partial cache failures.
type SuiteReplayPass struct {
	Pages          []*harvester.TranscriptIngestReport `json:"pages"`
	Scanned        int                                 `json:"scanned"`
	Stored         int                                 `json:"stored"`
	AlreadyPresent int                                 `json:"already_present"`
	Skipped        int                                 `json:"skipped"`
	Complete       bool                                `json:"complete"`
}

func verifySuiteTranscript(ctx context.Context, dir string, result *SuiteCase) error {
	if err := util.MkdirSecure(dir, 0o700); err != nil {
		return err
	}
	cache := filepath.Join(dir, "events")
	result.Ingestion = &SuiteReplayPass{}
	if err := suiteTranscriptPass(ctx, *result.Transcript, cache, result.Ingestion); err != nil {
		return fmt.Errorf("ingestion: %w", err)
	}
	first := result.Ingestion
	if first.Stored < 1 || first.AlreadyPresent != 0 {
		return errors.New("fresh transcript cache requires at least one newly stored observation")
	}
	result.Replay = &SuiteReplayPass{}
	if err := suiteTranscriptPass(ctx, *result.Transcript, cache, result.Replay); err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	replay := result.Replay
	if replay.Stored != 0 || replay.AlreadyPresent != first.Stored || replay.Scanned != first.Scanned || replay.Skipped != first.Skipped {
		return errors.New("transcript replay changed cache or counters")
	}
	if first.Pages[0].Source != replay.Pages[0].Source {
		return errors.New("transcript source changed between ingestion and replay")
	}
	return nil
}

func suiteTranscriptPass(ctx context.Context, source SuiteTranscript, cache string, pass *SuiteReplayPass) error {
	opts := harvester.TranscriptIngestOptions{Format: source.Format, SourcePath: source.SourcePath, ExpectedSHA256: source.SHA256, CacheDir: cache, MaxRecords: harvester.MaxTranscriptBatchRecords}
	for page := 0; page < maxSuitePages; page++ {
		report, err := harvester.IngestTranscript(ctx, opts)
		if report != nil {
			pass.Pages = append(pass.Pages, report)
		}
		if err == nil {
			err = validateSuitePage(source, pass, report)
		}
		if report != nil {
			accumulateSuitePage(pass, report)
		}
		if err != nil {
			return err
		}
		if report.Complete {
			pass.Complete = true
			return nil
		}
		if report.NextCursor == opts.Cursor {
			return errors.New("transcript cursor made no progress")
		}
		opts.Cursor = report.NextCursor
	}
	return errors.New("transcript did not complete within the bounded page count")
}

func accumulateSuitePage(pass *SuiteReplayPass, report *harvester.TranscriptIngestReport) {
	pass.Scanned += report.Scanned
	pass.Stored += report.Stored
	pass.AlreadyPresent += report.AlreadyPresent
	pass.Skipped += report.Skipped
}

func validateSuitePage(source SuiteTranscript, pass *SuiteReplayPass, report *harvester.TranscriptIngestReport) error {
	if report == nil {
		return errors.New("transcript ingestion returned no report")
	}
	if report.Source.SHA256 != source.SHA256 || report.Source.Format != source.Format {
		return errors.New("transcript report does not match the pinned source/format")
	}
	first := pass.Pages[0]
	if report.Source != first.Source || report.TotalRecords != first.TotalRecords {
		return errors.New("transcript changed across pages")
	}
	if report.TotalRecords < 1 || report.TotalRecords > harvester.MaxTranscriptRecords || report.TruncatedRecords != 0 {
		return errors.New("transcript must contain complete, nontruncated input records")
	}
	return validateSuiteCounters(pass, report)
}

func validateSuiteCounters(pass *SuiteReplayPass, report *harvester.TranscriptIngestReport) error {
	if report.Scanned < 1 || report.Scanned > harvester.MaxTranscriptBatchRecords || report.Stored < 0 || report.AlreadyPresent < 0 || report.Skipped < 0 || report.Scanned != report.Stored+report.AlreadyPresent+report.Skipped {
		return errors.New("invalid transcript page counters")
	}
	remaining := report.TotalRecords - pass.Scanned - report.Scanned
	if remaining < 0 || report.Remaining != remaining || report.Complete != (remaining == 0) || (report.NextCursor == "") != report.Complete {
		return errors.New("transcript page completion is inconsistent")
	}
	return nil
}
