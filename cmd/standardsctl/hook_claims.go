// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"sync"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// hookClaimDesk builds the claim desk the pre-dispatch hook reads claims through; tests
// replace it with a fake forge. The hook reaches the forge only for a brief that names an
// issue, so a dispatch without one needs no token and no network.
var hookClaimDesk = func(ctx context.Context, settings config.ForgeSettings) (*forge.ClaimDesk, error) {
	token := resolveForgeAuthToken(ctx, "")
	if token == "" {
		return nil, errors.New("no forge token: set GITHUB_TOKEN or sign in with gh")
	}
	return forge.NewClaimDesk(token, "", settings.ClaimStaleWindow()), nil
}

// hookIssueClaims is the claim lookup of `praetorctl hook`: the desk is built on first use,
// and a build failure is the error of every lookup, so the gate fails closed.
func hookIssueClaims(settings config.ForgeSettings) agenthook.IssueClaimLookup {
	var once sync.Once
	var desk *forge.ClaimDesk
	var deskErr error
	return func(ctx context.Context, ref string) (*agenthook.IssueClaim, error) {
		once.Do(func() { desk, deskErr = hookClaimDesk(ctx, settings) })
		if deskErr != nil {
			return nil, deskErr
		}
		parsed, err := forge.ParseClaimRef(ref)
		if err != nil {
			return nil, err
		}
		claim, err := desk.LiveClaim(ctx, parsed)
		if err != nil || claim == nil {
			return nil, err
		}
		return &agenthook.IssueClaim{Session: claim.Session, Lane: claim.Lane, Branch: claim.Branch,
			Stage: claim.Stage, Updated: claim.Updated}, nil
	}
}
