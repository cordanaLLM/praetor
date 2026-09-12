package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func runHarvestTranscript(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("harvest transcript", flag.ContinueOnError)
	var opts harvester.TranscriptIngestOptions
	fs.StringVar(&opts.Format, "format", harvester.TranscriptFormatAntigravity, "Source format: antigravity-jsonl-v1 or claude-code-jsonl-v1")
	fs.StringVar(&opts.SourcePath, "source", "", "Explicit JSONL source; Antigravity prefers its full counterpart")
	fs.StringVar(&opts.CacheDir, "cache", "", "Explicit private local cache destination")
	fs.StringVar(&opts.Cursor, "cursor", "", "Opaque resume cursor from the previous page")
	fs.StringVar(&opts.ExpectedSHA256, "expected-sha256", "", "Require this exact selected-source SHA256")
	fs.IntVar(&opts.MaxRecords, "max-records", 1000, "Maximum records in this page (1..10000)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || opts.SourcePath == "" || opts.CacheDir == "" {
		return fmt.Errorf("harvest transcript requires --source and --cache and no positional arguments")
	}
	if opts.MaxRecords < 1 || opts.MaxRecords > harvester.MaxTranscriptBatchRecords {
		return fmt.Errorf("max-records must be between 1 and %d", harvester.MaxTranscriptBatchRecords)
	}
	report, ingestErr := harvester.IngestTranscript(ctx, opts)
	if report != nil {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return errors.Join(ingestErr, fmt.Errorf("encode transcript report: %w", err))
		}
	}
	return ingestErr
}
