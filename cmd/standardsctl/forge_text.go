// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. The explicit repository determines the trusted owner;
// a missing origin trusts no extra owner. Invalid manifests and failed reads are errors.
func forgeTextPolicy(ctx context.Context, repo, manifestPath string, policy forge.ClosingReferencePolicy) (forge.ClosingReferencePolicy, error) {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return policy, err
	}
	if manifest != nil && manifest.Forge != nil {
		policy.OwnOrganizations = append(policy.OwnOrganizations, manifest.Forge.OwnOrganizations...)
	}
	owner, err := forgeTextOwner(ctx, repo)
	if err != nil {
		return policy, err
	}
	if owner != "" {
		policy.OwnOrganizations = append(policy.OwnOrganizations, owner)
	}
	return policy, nil
}

func forgeTextOwner(ctx context.Context, repo string) (string, error) {
	if identity := os.Getenv("GITHUB_REPOSITORY"); identity != "" {
		owner, name, _ := strings.Cut(identity, "/")
		if err := util.ValidateGitHubRepositoryIdentity(owner, name); err != nil {
			return "", fmt.Errorf("GITHUB_REPOSITORY: %w", err)
		}
		return owner, nil
	}
	owner, _, err := util.ResolveRemoteIdentity(ctx, repo)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return "", nil
	}
	return owner, err
}

func forgePolicyForCI(ctx context.Context, repo, manifestPath, token, endpoint, author string) (forge.ClosingReferencePolicy, error) {
	policy, err := closingPolicyForCI(ctx, repo, token, endpoint, author)
	if err != nil {
		return policy, err
	}
	return forgeTextPolicy(ctx, repo, manifestPath, policy)
}
