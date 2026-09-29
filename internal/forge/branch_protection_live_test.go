package forge

import (
	"net/http"
	"strings"
	"testing"
)

// protectionForge serves the three reads of ReadBranchProtection for acme/widgets: the active
// rules of the branch, the ruleset listing and the legacy protection object, answered with
// legacyStatus and legacy.
func protectionForge(t *testing.T, rules []map[string]any, legacyStatus int, legacy any) (*GitHubDriver, *fakeForgeServer) {
	t.Helper()
	return newFakeForge(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/rules/branches/"):
			writeJSON(t, w, http.StatusOK, rules)
		case r.URL.Path == "/repos/acme/widgets/rulesets":
			writeJSON(t, w, http.StatusOK, []map[string]any{{"id": 7, "name": RepositoryRulesetName}})
		case strings.HasSuffix(r.URL.Path, "/protection"):
			writeJSON(t, w, legacyStatus, legacy)
		default:
			writeJSON(t, w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		}
	})
}

// Positive: both mechanisms are read. The rules come with their ruleset named from the listing,
// and a legacy protection object is decoded.
func TestGitHubDriver_ReadBranchProtection_Positive_ReadsRulesetsAndLegacyProtection(t *testing.T) {
	rules := []map[string]any{
		{"type": "required_signatures", "ruleset_id": 7, "ruleset_source_type": "Repository", "ruleset_source": "acme/widgets"},
		{"type": "pull_request", "ruleset_id": 7, "ruleset_source_type": "Repository", "ruleset_source": "acme/widgets",
			"parameters": map[string]any{"required_approving_review_count": 1}},
	}
	legacy := map[string]any{
		"required_signatures":           map[string]any{"enabled": true},
		"required_pull_request_reviews": map[string]any{"required_approving_review_count": 2, "dismiss_stale_reviews": true},
		"required_status_checks":        map[string]any{"contexts": []string{"CI"}, "checks": []any{map[string]any{"context": "CI", "app_id": 1}}},
	}
	gh, fake := protectionForge(t, rules, http.StatusOK, legacy)
	live, err := gh.ReadBranchProtection(t.Context(), "release/1.x")
	if err != nil {
		t.Fatalf("ReadBranchProtection: %v", err)
	}
	if len(live.Rules) != 2 || live.Legacy == nil || !live.Legacy.RequiredSignatures.enabled() ||
		live.Legacy.RequiredPullRequestReviews.RequiredApprovingReviewCount != 2 {
		t.Fatalf("live protection = %+v (legacy %+v)", live, live.Legacy)
	}
	if got := live.Mechanisms(); len(got) != 2 || got[0] != `ruleset "praetor-main-protection" #7` || got[1] != LegacyProtectionMechanism {
		t.Fatalf("mechanisms = %v", got)
	}
	// The branch is one path segment, escaped, and nothing is written.
	for _, req := range fake.requests {
		if req.Method != http.MethodGet {
			t.Fatalf("a read wrote %s %s", req.Method, req.Path)
		}
	}
	if escaped := fake.requests[0].Escaped; escaped != "/repos/acme/widgets/rules/branches/release%2F1.x" {
		t.Fatalf("rules path = %s", escaped)
	}
}

// Boundary: GitHub's "Branch not protected" 404 is a branch without a legacy protection object,
// not an error; with no active rule either, the ruleset listing is not read at all. Its "Branch
// not found" 404 is a branch that does not exist yet, which has no legacy object either.
func TestGitHubDriver_ReadBranchProtection_Boundary_NoProtectionObject(t *testing.T) {
	gh, fake := protectionForge(t, []map[string]any{}, http.StatusNotFound, map[string]any{"message": legacyProtectionAbsent})
	live, err := gh.ReadBranchProtection(t.Context(), "main")
	if err != nil {
		t.Fatalf("ReadBranchProtection: %v", err)
	}
	if live.Legacy != nil || len(live.Rules) != 0 || len(live.Mechanisms()) != 0 {
		t.Fatalf("an unprotected branch read as %+v", live)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("expected the rules and the legacy read only, got %+v", fake.requests)
	}
	if live.Missing {
		t.Fatal("a branch GitHub reports as not protected exists")
	}

	// A branch GitHub does not have yet, such as a default branch not pushed yet, carries no
	// legacy protection object, and the rules of the rulesets that target its name still count.
	rules := []map[string]any{{"type": "deletion", "ruleset_id": 7, "ruleset_source_type": "Repository", "ruleset_source": "acme/widgets"}}
	gh, _ = protectionForge(t, rules, http.StatusNotFound, map[string]any{"message": legacyBranchAbsent})
	live, err = gh.ReadBranchProtection(t.Context(), "main")
	if err != nil {
		t.Fatalf("ReadBranchProtection of a missing branch: %v", err)
	}
	if !live.Missing || live.Legacy != nil || len(live.Rules) != 1 {
		t.Fatalf("a missing branch read as %+v", live)
	}
}

// Negative: any other 404 (such as a token that may not see the repository), a refusal, a
// malformed body, an empty branch and a missing token are errors, never an unprotected branch.
func TestGitHubDriver_ReadBranchProtection_Negative_UnreadableProtection(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   any
		want   string
	}{
		"not found":  {http.StatusNotFound, map[string]any{"message": "Not Found"}, "unexpected status 404"},
		"forbidden":  {http.StatusForbidden, map[string]any{"message": "Resource not accessible by integration"}, "unexpected status 403"},
		"malformed":  {http.StatusOK, []string{"not", "an", "object"}, "failed parsing branch protection"},
		"no message": {http.StatusNotFound, []string{"Branch not found"}, "legacy branch protection of main"},
	} {
		gh, _ := protectionForge(t, nil, tc.status, tc.body)
		if _, err := gh.ReadBranchProtection(t.Context(), "main"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want error containing %q, got %v", name, tc.want, err)
		}
	}
	failing, _ := newFakeForge(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeJSON(t, w, http.StatusOK, map[string]any{"rules": "not a list"})
	})
	if _, err := failing.ReadBranchProtection(t.Context(), "main"); err == nil || !strings.Contains(err.Error(), "active rules of main") {
		t.Errorf("malformed rules listing: got %v", err)
	}
	gh, _ := protectionForge(t, nil, http.StatusOK, map[string]any{})
	if _, err := gh.ReadBranchProtection(t.Context(), ""); err == nil {
		t.Error("an empty branch must be an error")
	}
	gh.Token = ""
	if _, err := gh.ReadBranchProtection(t.Context(), "main"); err == nil {
		t.Error("a missing token must be an error")
	}
}
