// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// Order: engine-fr-rule-checks-20261027. The explicit repository determines the trusted owner;
// a missing origin trusts no extra owner. Invalid manifests and failed reads are errors.
func forgeTextPolicy(ctx context.Context, repo, manifestPath string, policy forge.ClosingReferencePolicy) (forge.ClosingReferencePolicy, error) {
	own, err := forge.ResolveOwnOrganizations(ctx, repo, manifestPath, policy.OwnOrganizations)
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
