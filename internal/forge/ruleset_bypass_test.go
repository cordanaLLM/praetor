package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// adminPullRequestBypass is the bypass_actors value a single_maintainer ruleset carries, as
// encoding/json decodes it.
var adminPullRequestBypass = []any{map[string]any{"actor_id": float64(5), "actor_type": "RepositoryRole", "bypass_mode": "pull_request"}}

func renderedBypass(t *testing.T, mode config.BranchReviewMode) (any, bool) {
	t.Helper()
	policy := config.DefaultPolicy().BranchProtection
	policy.ReviewMode = mode
	data, err := RenderRepositoryRuleset("main", policy, []string{"Merge gate"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	actors, present := doc["bypass_actors"]
	return actors, present
}

// Positive (#76): under review_mode single_maintainer the rendered ruleset lets the repository
// admin role bypass its rules on a pull request, so the one maintainer cannot be locked out of
// the branch. Negative: independent review, declared or left at its default, renders no bypass
// actor, so every rule binds everyone.
func TestRenderRepositoryRuleset_BypassFollowsReviewMode(t *testing.T) {
	actors, present := renderedBypass(t, config.BranchReviewModeSingleMaintainer)
	if !present || !reflect.DeepEqual(actors, adminPullRequestBypass) {
		t.Fatalf("single_maintainer bypass_actors = %v (present %v), want %v", actors, present, adminPullRequestBypass)
	}
	for _, mode := range []config.BranchReviewMode{config.BranchReviewModeIndependent, ""} {
		if actors, present := renderedBypass(t, mode); present {
			t.Fatalf("review mode %q must render no bypass actor, got %v", mode, actors)
		}
	}
}

// Boundary: the bypass entry is part of the declared ruleset, so the committed file is drift
// without it under single_maintainer and with it under independent review; a file carrying exactly
// the rendering verifies.
func TestValidateRepositoryRuleset_Boundary_BypassIsDeclared(t *testing.T) {
	solo := config.DefaultPolicy().BranchProtection
	solo.ReviewMode = config.BranchReviewModeSingleMaintainer
	team := config.DefaultPolicy().BranchProtection
	contexts := []string{"Merge gate"}
	soloFile, err := RenderRepositoryRuleset("main", solo, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryRuleset(soloFile, "main", solo, contexts); err != nil {
		t.Fatalf("the single_maintainer rendering must verify: %v", err)
	}
	if err := ValidateRepositoryRuleset(soloFile, "main", team, contexts); !errors.Is(err, ErrRulesetDrift) {
		t.Fatalf("a bypass actor independent review does not declare must be drift, got %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(soloFile, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "bypass_actors")
	withoutBypass, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryRuleset(withoutBypass, "main", solo, contexts); !errors.Is(err, ErrRulesetDrift) {
		t.Fatalf("a single_maintainer ruleset without its bypass actor must be drift, got %v", err)
	}
}

// A ruleset sync --remote creates carries the bypass actor single_maintainer declares. A live
// ruleset keeps its own bypass actors: one without any gains none, and one with its own keeps
// them as they are, because adding or removing a bypass on a live ruleset is the operator's call.
func TestGitHubDriver_ReconcileProtection_BypassOnlyInANewRuleset(t *testing.T) {
	solo := &config.BranchProtectionPolicy{ReviewMode: config.BranchReviewModeSingleMaintainer}
	gh, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{}})
	if err := gh.ReconcileProtection(context.Background(), "main", solo); err != nil {
		t.Fatalf("create: %v", err)
	}
	create := fake.requests[1]
	if create.Method != http.MethodPost || !reflect.DeepEqual(create.Body["bypass_actors"], adminPullRequestBypass) {
		t.Fatalf("a created single_maintainer ruleset must carry the admin bypass: %s %s", create.Method, create.Raw)
	}

	gh, fake = rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 7, "name": "main-branch-protection"}}})
	if err := gh.ReconcileProtection(context.Background(), "main", solo); err != nil {
		t.Fatalf("update: %v", err)
	}
	if update := fake.requests[2]; update.Method != http.MethodPut || update.Body["bypass_actors"] != nil {
		t.Fatalf("a live ruleset without bypass actors must gain none: %s %s", update.Method, update.Raw)
	}

	team := []any{map[string]any{"actor_id": float64(9), "actor_type": "Team", "bypass_mode": "always"}}
	gh, fake = rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 7, "name": "main-branch-protection", "bypass_actors": team}}})
	if err := gh.ReconcileProtection(context.Background(), "main", solo); err != nil {
		t.Fatalf("update with live bypass: %v", err)
	}
	if update := fake.requests[2]; !reflect.DeepEqual(update.Body["bypass_actors"], team) {
		t.Fatalf("a live ruleset must keep its own bypass actors: %s", update.Raw)
	}
}
