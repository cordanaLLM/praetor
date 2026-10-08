// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/forge/forgetest"
)

func useFakeHookDesk(t *testing.T, fake *forgetest.ClaimFake, now time.Time) *int {
	t.Helper()
	built := 0
	previous := hookClaimDesk
	hookClaimDesk = func(context.Context, config.ForgeSettings) (*forge.ClaimDesk, error) {
		built++
		return &forge.ClaimDesk{Open: fake.Opener(), Stale: 6 * time.Hour, Now: func() time.Time { return now }}, nil
	}
	t.Cleanup(func() { hookClaimDesk = previous })
	return &built
}

func TestHookIssueClaims_Positive_ReportsTheLiveClaimAndBuildsTheDeskOnce(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	fake := forgetest.NewClaimFake()
	fake.Seed(forge.Claim{Session: "other", Lane: "agy", Branch: "feat/o", Stage: "review", Started: now, Updated: now}, "OWNER")
	built := useFakeHookDesk(t, fake, now.Add(time.Hour))
	lookup := hookIssueClaims(config.ForgeSettings{})
	for i := 0; i < 2; i++ {
		claim, err := lookup(context.Background(), "acme/widgets#7")
		if err != nil || claim == nil || claim.Session != "other" || claim.Stage != "review" || claim.Branch != "feat/o" {
			t.Fatalf("lookup %d: %+v %v", i, claim, err)
		}
	}
	if *built != 1 {
		t.Fatalf("desk built %d times, want once", *built)
	}
}

func TestHookIssueClaims_Boundary_StaleAndReleasedClaimsAreNoClaim(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	fake := forgetest.NewClaimFake()
	fake.Seed(forge.Claim{Session: "old", Lane: "l", Branch: "b", Stage: "review", Started: now.Add(-8 * time.Hour), Updated: now.Add(-6*time.Hour - time.Second)}, "OWNER")
	useFakeHookDesk(t, fake, now)
	if claim, err := hookIssueClaims(config.ForgeSettings{})(context.Background(), "acme/widgets#7"); err != nil || claim != nil {
		t.Fatalf("a claim one second past the window holds nothing: %+v %v", claim, err)
	}
}

func TestHookIssueClaims_Negative_FailsClosed(t *testing.T) {
	now := time.Now()
	fake := forgetest.NewClaimFake()
	useFakeHookDesk(t, fake, now)
	if _, err := hookIssueClaims(config.ForgeSettings{})(context.Background(), "widgets#7"); err == nil {
		t.Fatal("a malformed reference must be an error")
	}
	fake.FailOn = "ListIssueComments"
	if claim, err := hookIssueClaims(config.ForgeSettings{})(context.Background(), "acme/widgets#7"); !errors.Is(err, forge.ErrClaimUnverifiable) || claim != nil {
		t.Fatalf("a forge failure must not read as no claim: %+v %v", claim, err)
	}
	previous := hookClaimDesk
	hookClaimDesk = func(context.Context, config.ForgeSettings) (*forge.ClaimDesk, error) {
		return nil, errors.New("no forge token")
	}
	t.Cleanup(func() { hookClaimDesk = previous })
	if _, err := hookIssueClaims(config.ForgeSettings{})(context.Background(), "acme/widgets#7"); err == nil {
		t.Fatal("a desk that cannot be built must fail the lookup")
	}
}
