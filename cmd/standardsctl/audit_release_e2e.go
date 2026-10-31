// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/release"
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
func auditReleaseTestLevels(ctx context.Context, manifest *config.Manifest, rootDir string, flagRelease bool) error {
	line, _, err := release.AuditTestLevelsLine(ctx, rootDir, manifest, release.TestLevelsOptions{
		FlagRelease: flagRelease,
	})
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
