// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/readmegovernance"
	"github.com/cordanaLLM/praetor/internal/util"
)

// docsReadmeBaseline is the debt baseline the README block reports, beside the manifest.
const docsReadmeBaseline = ".standards-baseline.json"

// runDocsReadme renders the README governance block from the recorded debt baseline, the
// documentation facet and the manifest identity: the state `praetorctl audit` verifies the block
// against (readmeGovernanceState), so a rendered block passes the audit. It is the render
// command of the built-in "README governance block" generated artefact (ADR-0017); before it,
// only a whole `praetorctl adopt` run refreshed the block. A README without the block, or with
// the block declined by adoption.decline, is left alone.
func runDocsReadme(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("docs readme", flag.ContinueOnError)
	check := fs.Bool("check", false, "Report a stale block without writing; exit non-zero when it differs")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("docs readme accepts at most one repository path, got %q", positional)
	}
	repoPath := positionalAt(positional, 0, ".")
	manifest, err := config.LoadManifest(filepath.Join(repoPath, config.ManifestFileName))
	if err != nil {
		return fmt.Errorf("docs readme renders from the manifest: %w", err)
	}
	if declined, err := adopt.ManifestArtifactDeclined(manifest, "readme"); err != nil || declined {
		if declined {
			fmt.Println("README governance block declined by adoption.decline; nothing to render.")
		}
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(repoPath, readmegovernance.File))
	if err != nil {
		return err
	}
	if !exists || !hasReadmeBlock(string(data)) {
		fmt.Printf("README governance block not applicable: %s carries no block.\n", readmegovernance.File)
		return nil
	}
	rendered, changed, err := renderReadmeBlock(manifest, filepath.Join(repoPath, docsReadmeBaseline), string(data))
	if err != nil {
		return err
	}
	return applyReadmeBlock(repoPath, rendered, changed, *check)
}

// hasReadmeBlock reports whether content carries the governance block's markers; unbalanced
// markers count as carrying it, so Reconcile reports them instead of the file being skipped.
func hasReadmeBlock(content string) bool {
	first, _, err := util.FindMarkedBlock(content, readmegovernance.Start, readmegovernance.End)
	return err != nil || first >= 0
}

// renderReadmeBlock reconciles content with the state the baseline at baselinePath and the
// manifest give.
func renderReadmeBlock(manifest *config.Manifest, baselinePath, content string) (string, bool, error) {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return "", false, fmt.Errorf("load the debt baseline: %w", err)
	}
	state, err := readmeGovernanceState(manifest, base, !base.Absent, content)
	if err != nil {
		return "", false, fmt.Errorf("README governance state: %w", err)
	}
	return readmegovernance.Reconcile(content, state)
}

// applyReadmeBlock writes the rendered README, or, with check, fails when it differs.
func applyReadmeBlock(repoPath, rendered string, changed, check bool) error {
	switch {
	case !changed:
		fmt.Printf("[PASS] %s governance block is current.\n", readmegovernance.File)
		return nil
	case check:
		return errors.New("[FAIL] " + readmegovernance.File + " governance block is stale; run 'praetorctl docs readme' to render it")
	}
	if err := util.WriteFileConfined(repoPath, readmegovernance.File, []byte(rendered), util.TrackedFilePerm); err != nil {
		return fmt.Errorf("write %s: %w", readmegovernance.File, err)
	}
	fmt.Printf("Rendered the %s governance block.\n", readmegovernance.File)
	return nil
}
