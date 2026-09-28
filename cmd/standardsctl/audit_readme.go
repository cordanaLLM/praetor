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
	state, err := auditedReadmeState(manifest, opts, string(data))
	if err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w", err)
	}
	if err := readmegovernance.Verify(string(data), state); err != nil {
		return fmt.Errorf("[FAIL] README governance audit failed: %w; run praetorctl adopt to reconcile README.md", err)
	}
	fmt.Println("[PASS] README governance block verified against the recorded baseline.")
	return nil
}

// auditedReadmeState is the state audit verifies the README block against: the recorded
// baseline, the documentation facet and the manifest identity (adopt.ReadmeIdentity), with
// the forge host the block's AGENTS.md link records for that identity
// (readmegovernance.LinkedHost). Adoption takes that host from the origin remote; audit reads
// it back from content rather than from the clone's remote, so a mirror or a copy without
// one verifies the same block, and a link into another repository or onto an unknown host
// is stale.
func auditedReadmeState(manifest *config.Manifest, opts *auditOptions, content string) (readmegovernance.State, error) {
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
	owner, name, err := adopt.ReadmeIdentity(manifest, documentationEnabled)
	if err != nil {
		return readmegovernance.State{}, err
	}
	return readmegovernance.State{
		BaselineKnown:        opts.baselineKnown,
		LegacyDebtCount:      opts.baseline.Count(),
		DocumentationEnabled: documentationEnabled,
		RepositoryOwner:      owner,
		RepositoryName:       name,
		RepositoryHost:       readmegovernance.LinkedHost(content, owner, name),
	}, nil
}
