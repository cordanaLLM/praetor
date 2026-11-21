// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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
	doc, err := planFixtureRuleset(stub.rulesets)
	if err != nil {
		t.Fatal(err)
	}
	edit(doc)
}

// maxPlanFixtureRulesets bounds the ruleset map a plan fixture may scan.
const maxPlanFixtureRulesets = 32

// planFixtureRuleset finds the managed ruleset for editRuleset while its caller holds the lock.
func planFixtureRuleset(rulesets map[int]map[string]any) (map[string]any, error) {
	if len(rulesets) > maxPlanFixtureRulesets {
		return nil, fmt.Errorf("the plan fixture lists %d rulesets, more than %d", len(rulesets), maxPlanFixtureRulesets)
	}
	docs := slices.Collect(maps.Values(rulesets))
	for i := 0; i < len(docs) && i < maxPlanFixtureRulesets; i++ {
		doc := docs[i]
		if doc["name"] == "praetor-main-protection" {
			return doc, nil
		}
	}
	return nil, fmt.Errorf("no praetor ruleset in the stub: %v", rulesets)
}

// TestPlanFixtureRuleset_Boundary accepts the exact limit and refuses an oversized map.
func TestPlanFixtureRuleset_Boundary(t *testing.T) {
	rulesets := make(map[int]map[string]any, maxPlanFixtureRulesets+1)
	for i := 0; i < maxPlanFixtureRulesets; i++ {
		rulesets[i] = map[string]any{"name": "other"}
	}
	rulesets[maxPlanFixtureRulesets-1]["name"] = "praetor-main-protection"
	doc, err := planFixtureRuleset(rulesets)
	if err != nil || doc["name"] != "praetor-main-protection" {
		t.Fatalf("at fixture limit: %v, %v", doc, err)
	}
	rulesets[maxPlanFixtureRulesets] = map[string]any{"name": "other"}
	_, err = planFixtureRuleset(rulesets)
	mustErrContain(t, err, fmt.Sprintf("more than %d", maxPlanFixtureRulesets))
}

// TestPlan_FailOnDrift_Positive_SyncedBranchExitsZero checks both modes on synced fixtures.
func TestPlan_FailOnDrift_Positive_SyncedBranchExitsZero(t *testing.T) {
	fixtures := [...]func(*testing.T, *forgeStub) (*auditFixture, string){remoteProtectionFixture, aggregateRemoteFixture}
	for i := 0; i < len(fixtures) && i < 2; i++ {
		_, args := syncedPlanFixture(t, fixtures[i])
		assertPlanModes(t, args, false, []string{"[OK] Bypass actors: declared none, live none",
			"Status: GitHub enforces every declared branch protection property."}, []string{"[DRIFT]"})
	}
}

// TestPlan_FailOnDrift_Negative_PlantedDrift retains preview exits for every drift fixture.
func TestPlan_FailOnDrift_Negative_PlantedDrift(t *testing.T) {
	stub, args := syncedPlanFixture(t, remoteProtectionFixture)
	editRuleset(t, stub, func(doc map[string]any) {
		doc["bypass_actors"] = []any{map[string]any{"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}}
	})
	assertPlanModes(t, args, true, []string{"[DRIFT] Bypass actors: declared none, live [Team 42 always]", existingRulesetAction,
		`[DRIFT] The bypass actors of ruleset "praetor-main-protection" on main differ from the declared ones.`},
		[]string{"Status: GitHub enforces every declared branch protection property."})

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
	assertPlanModes(t, args, true, []string{"[DRIFT] Required status checks", "Action: Run 'praetorctl sync --remote' to reconcile branch protection on GitHub."},
		[]string{"Status: GitHub enforces every declared branch protection property."})
}

// TestPlan_FailOnDrift_Boundary rejects unread lists as clean in both modes and flag misuse.
func TestPlan_FailOnDrift_Boundary(t *testing.T) {
	stub, args := syncedPlanFixture(t, remoteProtectionFixture)
	editRuleset(t, stub, func(doc map[string]any) { delete(doc, "bypass_actors") })
	assertPlanModes(t, args, true, []string{"[DRIFT] Bypass actors: declared none, live unread (GitHub returns bypass_actors only with write access to the ruleset)", existingRulesetAction},
		[]string{"[OK] Bypass actors", "Status: GitHub enforces every declared branch protection property."})
	_, err := runPlanCmd(t, args[0], "--fail-on-drift")
	mustErrContain(t, err, "--fail-on-drift judges the live branch protection, which only --remote reads")
}

// singleMaintainerPlanFixture declares the admin bypass actor before syncing the stub.
func singleMaintainerPlanFixture(t *testing.T, stub *forgeStub) (*auditFixture, string) {
	t.Helper()
	f, endpoint := remoteProtectionFixture(t, stub)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+"overrides:\n  branch_protection:\n    review_mode: single_maintainer\n")
	policy := config.DefaultPolicy().BranchProtection
	policy.ReviewMode = config.BranchReviewModeSingleMaintainer
	if err := synthesizeRuleset(f.dir, "main", policy, nil); err != nil {
		t.Fatal(err)
	}
	return f, endpoint
}

// assertPlanModes checks the same report with both preview and failure exit behavior.
func assertPlanModes(t *testing.T, args []string, drift bool, want, absent []string) {
	t.Helper()
	for mode := 0; mode < 2; mode++ {
		runArgs := slices.Clone(args)
		if mode == 1 {
			runArgs = append(runArgs, "--fail-on-drift")
		}
		out, err := runPlanCmd(t, runArgs...)
		if drift && mode == 1 {
			if !errors.Is(err, ErrPlanDrift) {
				t.Fatalf("flagged drift: got %v\n%s", err, out)
			}
			if code := commandExitCode(io.Discard, err); code != 1 {
				t.Fatalf("flagged drift exit status: got %d, want 1", code)
			}
		} else if err != nil {
			t.Fatalf("mode %d: want nil error, got %v\n%s", mode, err, out)
		}
		mustContain(t, out, want...)
		rejectPlanMessages(t, out, absent)
	}
}

// rejectPlanMessages refuses false clean reports and remedies for the wrong ruleset state.
func rejectPlanMessages(t *testing.T, out string, absent []string) {
	t.Helper()
	if len(absent) > 4 {
		t.Fatal("too many forbidden plan messages")
	}
	for i := 0; i < len(absent) && i < 4; i++ {
		if strings.Contains(out, absent[i]) {
			t.Fatalf("unexpected %q\n%s", absent[i], out)
		}
	}
}

// TestPlan_FailOnDrift_MissingManagedRuleset distinguishes missing rulesets from live actors.
func TestPlan_FailOnDrift_MissingManagedRuleset(t *testing.T) {
	cases := [...]struct {
		name   string
		change func(map[string]any)
		clear  bool
		drift  bool
		want   []string
		absent []string
	}{
		{"protection elsewhere", func(doc map[string]any) { doc["name"] = "other-protection" }, false, true,
			[]string{"[DRIFT] Bypass actors: declared [RepositoryRole 5 pull_request], live no praetor-main-protection ruleset", missingRulesetAction},
			[]string{"by hand", "[DRIFT] GitHub does not enforce"}},
		{"no protection", func(map[string]any) {}, true, true,
			[]string{"[DRIFT] Bypass actors: declared [RepositoryRole 5 pull_request], live no praetor-main-protection ruleset", missingRulesetAction,
				"[DRIFT] GitHub does not enforce the declared"}, []string{"by hand"}},
		{"extra actor", func(doc map[string]any) {
			doc["bypass_actors"] = []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "pull_request"},
				map[string]any{"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}}
		}, false, true, []string{"[DRIFT] Bypass actors: declared [RepositoryRole 5 pull_request], live [RepositoryRole 5 pull_request, Team 42 always]", existingRulesetAction}, []string{missingRulesetAction}},
		{"equal", func(map[string]any) {}, false, false,
			[]string{"[OK] Bypass actors: declared [RepositoryRole 5 pull_request], live [RepositoryRole 5 pull_request]", "Status: GitHub enforces every declared branch protection property."},
			[]string{"[DRIFT]", existingRulesetAction, missingRulesetAction}},
		{"missing actor", func(doc map[string]any) { doc["bypass_actors"] = []any{} }, false, true,
			[]string{"[DRIFT] Bypass actors: declared [RepositoryRole 5 pull_request], live none", existingRulesetAction}, []string{missingRulesetAction}},
	}
	for i := 0; i < len(cases) && i < 5; i++ {
		c := cases[i]
		t.Run(c.name, func(t *testing.T) {
			stub, args := syncedPlanFixture(t, singleMaintainerPlanFixture)
			editRuleset(t, stub, c.change)
			if c.clear {
				stub.mu.Lock()
				clear(stub.rulesets)
				stub.mu.Unlock()
			}
			assertPlanModes(t, args, c.drift, c.want, c.absent)
		})
	}
}

// missingRulesetAction names creation; existingRulesetAction preserves the live-actor remedy.
const (
	missingRulesetAction  = `Action: Run 'praetorctl sync --remote' to create missing ruleset "praetor-main-protection" with the declared bypass actors.`
	existingRulesetAction = `sync --remote keeps the bypass actors of a live ruleset; change those of ruleset "praetor-main-protection" on GitHub by hand (repository Settings, Rules) if the declared policy is intended`
)

// plantRulesetScope places a same-named parent before or after the repository ruleset.
// The inherited-only case keeps all protection rules to isolate managed ruleset absence.
func plantRulesetScope(t *testing.T, stub *forgeStub, scenario string) {
	t.Helper()
	stub.mu.Lock()
	defer stub.mu.Unlock()
	repo, err := planFixtureRuleset(stub.rulesets)
	if err != nil {
		t.Fatal(err)
	}
	parent := maps.Clone(repo)
	repo["id"], parent["id"] = 2, 1
	parent["source_type"], parent["source"] = "Organization", "acme"
	parent["bypass_actors"] = []any{}
	repo["source_type"], repo["source"] = "Repository", "acme/widgets"
	clear(stub.rulesets)
	stub.rulesets[2] = repo
	switch scenario {
	case "only inherited":
		delete(stub.rulesets, 2)
		stub.rulesets[1] = parent
	case "unread scope":
		repo["source_type"], repo["source"] = nil, nil
	case "duplicate repository names":
		stub.rulesets[4] = maps.Clone(repo)
		stub.rulesets[4]["id"] = 4
	case "only repository":
	default:
		parent["rules"] = []any{map[string]any{"type": "deletion"}}
		repo["bypass_actors"] = []any{map[string]any{"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}}
		stub.reverseRulesets = scenario == "repository first"
		stub.rulesets[1] = parent
	}
}

// TestPlan_FailOnDrift_RulesetScope judges repository actors in both orders and preserves absence behavior.
func TestPlan_FailOnDrift_RulesetScope(t *testing.T) {
	cases := [...]string{"parent first", "repository first", "only inherited", "only repository"}
	for i := 0; i < len(cases) && i < 4; i++ {
		name := cases[i]
		t.Run(name, func(t *testing.T) {
			fixture := remoteProtectionFixture
			if name == "only inherited" {
				fixture = singleMaintainerPlanFixture
			}
			stub, args := syncedPlanFixture(t, fixture)
			plantRulesetScope(t, stub, name)
			want := []string{"[DRIFT] Bypass actors: declared none, live [Team 42 always]", existingRulesetAction}
			if name == "only inherited" {
				want = []string{"live no praetor-main-protection ruleset", missingRulesetAction}
			}
			if name == "only repository" {
				want = []string{"[OK] Bypass actors: declared none, live none", "Status: GitHub enforces every declared branch protection property."}
			}
			assertPlanModes(t, args, name != "only repository", want, []string{"[DRIFT] GitHub does not enforce"})
		})
	}
}

// TestPlan_FailOnDrift_UnreadRulesetScope refuses unknown owners and duplicate repository names.
func TestPlan_FailOnDrift_UnreadRulesetScope(t *testing.T) {
	cases := [...]struct{ name, diagnostic string }{
		{"unread scope", "unread scope"}, {"duplicate repository names", "ids 2 and 4"},
	}
	for i := 0; i < len(cases) && i < 2; i++ {
		c := cases[i]
		t.Run(c.name, func(t *testing.T) {
			stub, args := syncedPlanFixture(t, remoteProtectionFixture)
			plantRulesetScope(t, stub, c.name)
			for mode := 0; mode < 2; mode++ {
				runArgs := slices.Clone(args)
				if mode == 1 {
					runArgs = append(runArgs, "--fail-on-drift")
				}
				out, err := runPlanCmd(t, runArgs...)
				mustErrContain(t, err, c.diagnostic)
				rejectPlanMessages(t, out, []string{"[OK] Bypass actors", "Status: GitHub enforces every declared branch protection property."})
			}
		})
	}
}

// TestSync_Remote_RulesetScope updates repository id 2, preserving inherited id 1 in both orders.
func TestSync_Remote_RulesetScope(t *testing.T) {
	for _, name := range [2]string{"parent first", "repository first"} {
		t.Run(name, func(t *testing.T) {
			stub, args := syncedPlanFixture(t, remoteProtectionFixture)
			plantRulesetScope(t, stub, name)
			before := len(stub.recorded())
			out, err := runSyncCmd(t, args...)
			if err != nil {
				t.Fatalf("sync: %v\n%s", err, out)
			}
			writes := stub.recorded()[before:]
			if !slices.Contains(writes, "PUT /repos/acme/widgets/rulesets/2") {
				t.Fatalf("repository id 2 not updated: %v", writes)
			}
			if slices.Contains(writes, "PUT /repos/acme/widgets/rulesets/1") {
				t.Fatalf("inherited ruleset updated: %v", writes)
			}
		})
	}
}
