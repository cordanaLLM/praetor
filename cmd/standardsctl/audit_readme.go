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
	declined, err := adopt.ManifestArtifactDeclined(manifest, "readme")
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
	state, err := auditedReadmeState(manifest, opts)
	if err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w", err)
	}
	if err := readmegovernance.Verify(string(data), state); err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w; run praetorctl adopt to reconcile README.md", err)
	}
	fmt.Println("[PASS] README governance block verified against the recorded baseline.")
	return nil
}

func auditedReadmeState(manifest *config.Manifest, opts *auditOptions) (readmegovernance.State, error) {
	if opts.baseline == nil {
		return readmegovernance.State{}, fmt.Errorf("baseline gate did not provide a snapshot")
	}
	documentationEnabled := false
	if manifest != nil {
		var err error
		documentationEnabled, err = adopt.DocumentationEnabled(manifest.Facets)
		if err != nil {
			return readmegovernance.State{}, fmt.Errorf("resolve documentation facet: %w", err)
		}
	}
	state := readmegovernance.State{
		BaselineKnown:        opts.baselineKnown,
		LegacyDebtCount:      opts.baseline.Count(),
		DocumentationEnabled: documentationEnabled,
	}
	if state.DocumentationEnabled {
		state.RepositoryOwner = manifest.Repository.Owner
		state.RepositoryName = manifest.Repository.Name
	}
	return state, nil
}
