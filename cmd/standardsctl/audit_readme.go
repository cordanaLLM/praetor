package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

func auditReadmeGovernance(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	var declines []string
	if manifest != nil && manifest.Adoption != nil {
		declines = manifest.Adoption.Decline
	}
	declined, err := adopt.ArtifactDeclined(declines, "readme")
	if err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w", err)
	}
	if declined {
		fmt.Println("[INFO] README governance block declined by adoption.decline.")
		return nil
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(opts.rootDir, "README.md"))
	if err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w", err)
	}
	if !exists {
		fmt.Println("[INFO] README governance block not applicable: README.md is absent.")
		return nil
	}
	if opts.baseline == nil {
		return fmt.Errorf("[FAIL] README governance audit failed: baseline gate did not provide a snapshot")
	}
	state := readmegovernance.State{
		BaselineKnown:   opts.baselineKnown,
		LegacyDebtCount: opts.baseline.Count(),
	}
	if err := readmegovernance.Verify(string(data), state); err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w; run praetorctl adopt to reconcile README.md", err)
	}
	fmt.Println("[PASS] README governance block verified against the recorded baseline.")
	return nil
}
