package forge

import (
	"net/http"
	"strings"
	"testing"
)

// LiveRuleset returns the named ruleset as GitHub reports it, so a caller can see a required
// check the merge kept although it no longer asked for it.
//
// Positive: the praetor ruleset is listed and read back, and its required check is visible.
// Boundary: no ruleset of that name is nil without an error, after the listing alone.
// Negative: an empty branch, an empty token and a failed read are errors, never a nil ruleset.
func TestGitHubDriver_LiveRuleset(t *testing.T) {
	live := map[string]any{
		"id": 7, "name": RepositoryRulesetName,
		"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"required_status_checks": []any{map[string]any{"context": "Platform Neutrality (Linux)"}},
		}}},
	}
	gh, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 3, "name": "other"}, live}})
	gh.RulesetName = RepositoryRulesetName
	body, err := gh.LiveRuleset(t.Context(), "main")
	if err != nil {
		t.Fatalf("LiveRuleset: %v", err)
	}
	required, err := RulesetRequiresStatusContext(body, "Platform Neutrality (Linux)")
	if err != nil || !required {
		t.Fatalf("live ruleset requires the leg = %v (%v), want true: %s", required, err, body)
	}
	if len(fake.requests) != 2 || fake.requests[1].Path != "/repos/acme/widgets/rulesets/7" {
		t.Fatalf("expected a listing and one read, got %+v", fake.requests)
	}

	absent, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 3, "name": "other"}}})
	absent.RulesetName = RepositoryRulesetName
	if body, err := absent.LiveRuleset(t.Context(), "main"); err != nil || body != nil {
		t.Fatalf("absent ruleset = %q (%v), want nil without an error", body, err)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("absent ruleset: expected the listing alone, got %+v", fake.requests)
	}

	if _, err := gh.LiveRuleset(t.Context(), ""); err == nil {
		t.Error("an empty branch was accepted")
	}
	tokenless := NewGitHubDriver("", "http://127.0.0.1:1")
	tokenless.SetRepository("acme", "widgets")
	if _, err := tokenless.LiveRuleset(t.Context(), "main"); err == nil {
		t.Error("an empty token was accepted")
	}
	failing, _ := newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if strings.HasSuffix(r.URL.Path, "/rulesets") {
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 7, "name": RepositoryRulesetName}})
			return
		}
		writeJSON(t, w, http.StatusInternalServerError, map[string]any{"message": "boom"})
	})
	failing.RulesetName = RepositoryRulesetName
	if body, err := failing.LiveRuleset(t.Context(), "main"); err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("failed read = %q (%v), want the status 500 error", body, err)
	}
}
