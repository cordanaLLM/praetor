// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"os"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// forgeTextPolicy resolves the trusted forge organisations for repo and manifestPath and
// stores them on policy. A missing manifest or origin adds no extra owner; invalid manifests
// and failed reads are refused with an error (Order: engine-fr-rule-checks-20261027).
func forgeTextPolicy(ctx context.Context, repo, manifestPath string, policy forge.ClosingReferencePolicy) (forge.ClosingReferencePolicy, error) {
	own, err := forge.ResolveOwnOrganizations(ctx, repo, manifestPath, policy.OwnOrganizations, os.Getenv)
	if err != nil {
		return policy, err
	}
	policy.OwnOrganizations = own
	return policy, nil
}

func forgePolicyForCI(ctx context.Context, repo, manifestPath, token, endpoint, author string) (forge.ClosingReferencePolicy, error) {
	policy, err := closingPolicyForCI(ctx, repo, token, endpoint, author)
	if err != nil {
		return policy, err
	}
	return forgeTextPolicy(ctx, repo, manifestPath, policy)
}
