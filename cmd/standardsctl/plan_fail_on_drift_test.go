// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"slices"
	"testing"
)

// syncedPlanFixture syncs an acme/widgets fixture to the stub forge and returns the plan
// arguments of a --remote run against it.
func syncedPlanFixture(t *testing.T, fixture func(*testing.T, *forgeStub) (*auditFixture, string)) (*forgeStub, []string) {
	t.Helper()
	stub := &forgeStub{}
	f, endpoint := fixture(t, stub)
	if out, err := runSyncCmd(t, "--config="+f.manifestPath, "--remote", "--token=ghp_x", "--endpoint="+endpoint); err != nil {
		t.Fatalf("remote sync: %v\n%s", err, out)
	}
	return stub, []string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + endpoint}
}

// editRuleset changes the praetor ruleset the stub stores, as a hand edit on GitHub would.
func editRuleset(t *testing.T, stub *forgeStub, edit func(map[string]any)) {
	t.Helper()
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, doc := range stub.rulesets {
		if doc["name"] == "praetor-main-protection" {
			edit(doc)
			return
		}
	}
	t.Fatalf("no praetor ruleset in the stub: %v", stub.rulesets)
}

// Positive: a synced branch passes plan --remote --fail-on-drift, bypass actors included.
func TestPlan_FailOnDrift_Positive_SyncedBranchExitsZero(t *testing.T) {
	for _, fixture := range []func(*testing.T, *forgeStub) (*auditFixture, string){remoteProtectionFixture, aggregateRemoteFixture} {
		_, args := syncedPlanFixture(t, fixture)
		if out, err := runPlanCmd(t, append(args, "--fail-on-drift")...); err != nil {
			t.Fatalf("plan --remote --fail-on-drift on a synced branch: %v\n%s", err, out)
		}
	}
	_, args := syncedPlanFixture(t, remoteProtectionFixture)
	out, err := runPlanCmd(t, append(args, "--fail-on-drift")...)
	if err != nil {
		t.Fatalf("plan --remote --fail-on-drift on a synced branch: %v\n%s", err, out)
	}
	mustContain(t, out, "[OK] Bypass actors: declared none, live none",
		"Status: GitHub enforces every declared branch protection property.")
}

// Negative, planted: an extra bypass actor and a dropped required check are drift; with
// --fail-on-drift plan exits with ErrPlanDrift, without it the preview still exits zero.
func TestPlan_FailOnDrift_Negative_PlantedDrift(t *testing.T) {
	stub, args := syncedPlanFixture(t, remoteProtectionFixture)
	editRuleset(t, stub, func(doc map[string]any) {
		doc["bypass_actors"] = []any{map[string]any{"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}}
	})
	out, err := runPlanCmd(t, append(args, "--fail-on-drift")...)
	if !errors.Is(err, ErrPlanDrift) {
		t.Fatalf("an extra bypass actor: want ErrPlanDrift, got %v\n%s", err, out)
	}
	mustContain(t, out, "[DRIFT] Bypass actors: declared none, live [Team 42 always]",
		"sync --remote keeps the bypass actors of a live ruleset",
		`[DRIFT] The bypass actors of ruleset "praetor-main-protection" on main differ from the declared ones.`)
	if out, err := runPlanCmd(t, args...); err != nil {
		t.Fatalf("plan --remote without --fail-on-drift must stay a zero-exit preview: %v\n%s", err, out)
	}

	stub, args = syncedPlanFixture(t, aggregateRemoteFixture)
	editRuleset(t, stub, func(doc map[string]any) {
		rules, ok := doc["rules"].([]any)
		if !ok {
			t.Fatalf("stored ruleset rules: %T", doc["rules"])
		}
		doc["rules"] = slices.DeleteFunc(slices.Clone(rules), func(rule any) bool {
			typed, isMap := rule.(map[string]any)
			return isMap && typed["type"] == "required_status_checks"
		})
	})
	out, err = runPlanCmd(t, append(args, "--fail-on-drift")...)
	if !errors.Is(err, ErrPlanDrift) {
		t.Fatalf("a dropped required check: want ErrPlanDrift, got %v\n%s", err, out)
	}
	mustContain(t, out, "[DRIFT] Required status checks", "Action: Run 'praetorctl sync --remote' to reconcile branch protection on GitHub.")
}

// Boundary: an unread bypass list never passes, and --fail-on-drift without --remote is refused.
func TestPlan_FailOnDrift_Boundary(t *testing.T) {
	stub, args := syncedPlanFixture(t, remoteProtectionFixture)
	editRuleset(t, stub, func(doc map[string]any) { delete(doc, "bypass_actors") })
	out, err := runPlanCmd(t, append(args, "--fail-on-drift")...)
	if !errors.Is(err, ErrPlanDrift) {
		t.Fatalf("an unread bypass list: want ErrPlanDrift, got %v\n%s", err, out)
	}
	mustContain(t, out, "[DRIFT] Bypass actors: declared none, live unread")
	_, err = runPlanCmd(t, args[0], "--fail-on-drift")
	mustErrContain(t, err, "--fail-on-drift judges the live branch protection, which only --remote reads")
}
