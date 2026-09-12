package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// baselineScanTimeout bounds the debt-recording scan (HISS-02).
const baselineScanTimeout = 5 * time.Minute

func runBaseline(args []string) error {
	fs := flag.NewFlagSet("baseline", flag.ContinueOnError)
	baselinePath := fs.String("file", ".standards-baseline.json", "Path to baseline file")
	record := fs.Bool("record", false, "Record current infractions into baseline file")

	if err := fs.Parse(args); err != nil {
		return err
	}

	b, err := baseline.LoadBaseline(*baselinePath)
	if err != nil {
		return fmt.Errorf("failed to load baseline: %w", err)
	}

	if *record {
		return recordBaseline(*baselinePath, b)
	}

	fmt.Printf("=== cordanaLLM/praetor Technical Debt Baseline ===\n")
	fmt.Printf("File: %s | Total Infractions: %d\n", *baselinePath, b.TotalInfractions)
	for i, inf := range b.Infractions {
		fmt.Printf("  #%d [%s] %s:%d (%s) - %s\n", i+1, inf.RuleID, inf.FilePath, inf.LineNumber, inf.Symbol, inf.Message)
	}

	if b.TotalInfractions == 0 {
		fmt.Println("Zero technical debt recorded. Repository is 100% compliant.")
	}

	return nil
}

// recordBaseline rescans the repository that owns baselinePath and stores every current
// infraction as accepted debt. A capped scan is refused: a partial baseline would exempt
// every unscanned violation from the ratchet.
func recordBaseline(baselinePath string, b *baseline.Baseline) error {
	// HISS-02: the recording scan is bounded even when no caller context exists.
	ctx, cancel := context.WithTimeout(context.Background(), baselineScanTimeout)
	defer cancel()
	repoDir := filepath.Dir(baselinePath)
	if repoDir == "" {
		repoDir = "."
	}
	scanRep, err := hiss.Scan(ctx, repoDir, hiss.ScanOptions{})
	if err != nil {
		return fmt.Errorf("failed to scan for baseline infractions: %w", err)
	}
	if scanRep.Truncated {
		return fmt.Errorf("refusing to record an incomplete baseline: %w", hiss.ErrScanTruncated)
	}

	b.Infractions = make([]baseline.Infraction, 0, len(scanRep.Violations))
	for _, v := range scanRep.Violations {
		b.Infractions = append(b.Infractions, baseline.Infraction{
			RuleID:      v.RuleID,
			FilePath:    v.FilePath,
			LineNumber:  v.LineNumber,
			Symbol:      v.Symbol,
			Message:     v.Message,
			Fingerprint: fmt.Sprintf("%s:%d:%s", v.FilePath, v.LineNumber, v.RuleID),
		})
	}
	b.TotalInfractions = len(b.Infractions)

	if err := baseline.SaveBaseline(baselinePath, b); err != nil {
		return fmt.Errorf("failed to save baseline: %w", err)
	}
	fmt.Printf("Baseline successfully updated: %s (Total: %d infractions)\n", baselinePath, b.TotalInfractions)
	return nil
}
