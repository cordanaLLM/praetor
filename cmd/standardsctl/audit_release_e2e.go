// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/release"
	"github.com/cordanaLLM/praetor/internal/util"
)

// auditReleaseE2E checks that every feat or fix commit since the last release tag
// carries end-to-end test evidence (order engine-release-feature-e2e-gate-20261027).
func auditReleaseE2E(ctx context.Context, manifest *config.Manifest, rootDir string, flagRelease bool) error {
	var policy *config.ReleasePolicy
	if manifest != nil {
		policy = manifest.Release
	}
	line, err := release.AuditE2E(ctx, rootDir, policy, flagRelease)
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}

// auditReleaseTestLevels evaluates required and declared release test levels (#1733).
func auditReleaseTestLevels(ctx context.Context, manifest *config.Manifest, rootDir string, flagRelease, offline bool) error {
	var repo config.RepositoryMetadata
	if manifest != nil {
		repo = manifest.Repository
	}
	branch, branchErr := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	live := openActionsForge(ctx, rootDir, repo, offline)
	if branchErr != nil {
		live.reader = nil
	}
	opts := release.TestLevelsOptions{
		FlagRelease: flagRelease,
		Actions:     live.reader,
		Branch:      branch,
	}
	if flagRelease {
		headCtx, cancel := context.WithTimeout(ctx, util.GitProbeTimeout)
		defer cancel()
		head, err := util.ResolveGitCommit(headCtx, rootDir, "HEAD")
		if err != nil {
			return fmt.Errorf("resolve release test levels HEAD: %w", err)
		}
		opts.Head = head
	}
	line, _, err := release.AuditTestLevelsLine(ctx, rootDir, manifest, opts)
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
