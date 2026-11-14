// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

var singleMaintainer = config.BranchProtectionPolicy{ReviewMode: config.BranchReviewModeSingleMaintainer}

const adminPullRequestActor = `{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "pull_request"}`

// Positive: the live actors equal the declared ones, in any order; none declared and none live
// (or no ruleset at all) is enforced.
func TestEvaluateBypassActors_Positive(t *testing.T) {
	cases := []struct {
		name   string
		policy config.BranchProtectionPolicy
		body   []byte
	}{
		{"single maintainer admin role", singleMaintainer, []byte(`{"bypass_actors": [` + adminPullRequestActor + `]}`)},
		{"none declared, empty list", config.BranchProtectionPolicy{}, []byte(`{"bypass_actors": []}`)},
		{"none declared, no ruleset", config.BranchProtectionPolicy{}, nil},
	}
	for _, c := range cases {
		finding, err := EvaluateBypassActors(c.policy, c.body)
		if err != nil || finding.Verdict != ProtectionEnforced || finding.Property != BypassActorsProperty {
			t.Errorf("%s: finding %+v, error %v; want enforced", c.name, finding, err)
		}
	}
}

// Negative: an extra live actor, a missing declared one, a duplicated one, an unread list and a
// missing ruleset under a declared actor are drift.
func TestEvaluateBypassActors_Negative(t *testing.T) {
	cases := []struct {
		name   string
		policy config.BranchProtectionPolicy
		body   []byte
		live   string
	}{
		{"extra actor", singleMaintainer, []byte(`{"bypass_actors": [` + adminPullRequestActor + `, {"actor_id": 42, "actor_type": "Team", "bypass_mode": "always"}]}`),
			"[RepositoryRole 5 pull_request, Team 42 always]"},
		{"extra actor, none declared", config.BranchProtectionPolicy{}, []byte(`{"bypass_actors": [{"actor_id": null, "actor_type": "OrganizationAdmin", "bypass_mode": "always"}]}`),
			"[OrganizationAdmin - always]"},
		{"missing declared actor", singleMaintainer, []byte(`{"bypass_actors": []}`), "none"},
		{"wider mode", singleMaintainer, []byte(`{"bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}]}`),
			"[RepositoryRole 5 always]"},
		{"duplicated actor", singleMaintainer, []byte(`{"bypass_actors": [` + adminPullRequestActor + `, ` + adminPullRequestActor + `]}`),
			"[RepositoryRole 5 pull_request, RepositoryRole 5 pull_request]"},
		{"unread list", singleMaintainer, []byte(`{"name": "praetor-main-protection"}`), "unread (GitHub returns bypass_actors only with write access to the ruleset)"},
		{"no ruleset", singleMaintainer, nil, "no " + RepositoryRulesetName + " ruleset"},
	}
	for _, c := range cases {
		finding, err := EvaluateBypassActors(c.policy, c.body)
		if err != nil || finding.Verdict != ProtectionDrift || finding.Live != c.live {
			t.Errorf("%s: finding %+v, error %v; want drift with live %q", c.name, finding, err, c.live)
		}
	}
}

// Boundary: a malformed body and a list over the bound are errors, never a verdict.
func TestEvaluateBypassActors_Boundary(t *testing.T) {
	if _, err := EvaluateBypassActors(singleMaintainer, []byte(`{"bypass_actors": {}}`)); err == nil {
		t.Fatal("a malformed bypass_actors value was judged")
	}
	actors := make([]string, maxBypassActors+1)
	for i := range actors {
		actors[i] = fmt.Sprintf(`{"actor_id": %d, "actor_type": "Team", "bypass_mode": "always"}`, i)
	}
	_, err := EvaluateBypassActors(singleMaintainer, []byte(`{"bypass_actors": [`+strings.Join(actors, ",")+`]}`))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d", maxBypassActors)) {
		t.Fatalf("an over-bound list: %v", err)
	}
	finding, err := EvaluateBypassActors(singleMaintainer, []byte(`{"bypass_actors": [`+strings.Join(actors[:maxBypassActors], ",")+`]}`))
	if err != nil || finding.Verdict != ProtectionDrift {
		t.Fatalf("a list at the bound: %+v %v", finding, err)
	}
}

// TestBypassActorKeys_Boundary verifies that declared bypass actors exceeding maxBypassActors
// are rejected with an error rather than silently truncated.
func TestBypassActorKeys_Boundary(t *testing.T) {
	actors := make([]map[string]any, maxBypassActors+1)
	for i := range actors {
		actors[i] = map[string]any{"actor_id": int64(i), "actor_type": "Team", "bypass_mode": "always"}
	}
	if _, err := bypassActorKeys(actors); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d", maxBypassActors)) {
		t.Fatalf("an over-bound declared list: %v; want error naming the bound", err)
	}
	keys, err := bypassActorKeys(actors[:maxBypassActors])
	if err != nil || len(keys) != maxBypassActors {
		t.Fatalf("a declared list at the bound: keys=%v, err=%v", keys, err)
	}
}
