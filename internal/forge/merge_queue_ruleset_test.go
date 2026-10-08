// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Positive: the queue ruleset targets the default branch alone, is active and holds the
// merge_queue rule alone, with the merge method that keeps a linear history linear. Boundary: a
// default branch other than main is the include. Negative: a name that is no branch is refused.
func TestRenderMergeQueueRuleset(t *testing.T) {
	doc, err := RenderMergeQueueRuleset("master", config.BranchProtectionPolicy{EnforceLinearHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if doc["name"] != MergeQueueRulesetName || doc["enforcement"] != "active" {
		t.Fatalf("name/enforcement = %v/%v", doc["name"], doc["enforcement"])
	}
	if include, err := rulesetIncludes(doc); err != nil || !slices.Equal(include, []string{"refs/heads/master"}) {
		t.Fatalf("includes = %v (%v), want master alone", include, err)
	}
	rules, err := objectList(doc["rules"], "rules", maxRulesetRules)
	if err != nil || len(rules) != 1 || rules[0]["type"] != MergeQueueRule {
		t.Fatalf("rules = %v (%v), want the merge_queue rule alone", rules, err)
	}
	params, isObject := rules[0]["parameters"].(map[string]any)
	if !isObject || params["merge_method"] != "SQUASH" {
		t.Fatalf("linear history must merge by squash: %v", rules[0]["parameters"])
	}
}

func TestRenderMergeQueueRuleset_Negative_NotABranchName(t *testing.T) {
	if _, err := RenderMergeQueueRuleset("bad branch", config.BranchProtectionPolicy{}); err == nil {
		t.Fatal("a default branch that is no branch name rendered a ruleset")
	}
}

// The wildcard test GitHub's documented limit needs: patterns are wildcards, one branch and the
// default branch alias are not.
func TestLiveQueueRulesetWildcards(t *testing.T) {
	for includes, want := range map[string]int{
		"refs/heads/main":                  0,
		"~DEFAULT_BRANCH":                  0,
		"refs/heads/main,refs/heads/lts-*": 1,
		"refs/heads/rel?":                  1,
		"refs/heads/[ab]":                  1,
		"~ALL":                             1,
		"":                                 0,
	} {
		live := LiveQueueRuleset{Includes: strings.Split(includes, ",")}
		if got := len(live.Wildcards()); got != want {
			t.Errorf("%q: %d wildcards, want %d", includes, got, want)
		}
	}
}

// Positive and negative: a declared queue needs an active merge_queue rule on the branch and no
// wildcard in the queue ruleset; a policy without a queue is compared on neither.
func TestMergeQueueRulesetFindings(t *testing.T) {
	declared := config.BranchProtectionPolicy{MergeQueue: true}
	queued := LiveBranchProtection{Branch: "main", RulesetNames: map[int]string{2: MergeQueueRulesetName},
		Rules: []LiveBranchRule{{Type: MergeQueueRule, RulesetID: 2}}}
	if got := MergeQueueRulesetFindings(config.BranchProtectionPolicy{}, &queued); got != nil {
		t.Fatalf("an undeclared queue was compared: %+v", got)
	}
	if drifts := ProtectionDrifts(MergeQueueRulesetFindings(declared, &queued)); len(drifts) != 0 {
		t.Fatalf("a queue ruleset with a clean include drifted: %v", drifts)
	}
	absent := LiveBranchProtection{Branch: "main"}
	if drifts := ProtectionDrifts(MergeQueueRulesetFindings(declared, &absent)); !slices.Equal(drifts, []string{"Merge queue"}) {
		t.Fatalf("a missing queue drifts = %v", drifts)
	}
	queued.QueueRuleset = &LiveQueueRuleset{ID: 2, Includes: []string{"refs/heads/main", "refs/heads/lts-*"}}
	findings := MergeQueueRulesetFindings(declared, &queued)
	if drifts := ProtectionDrifts(findings); !slices.Equal(drifts, []string{"Merge queue ruleset refs"}) {
		t.Fatalf("a wildcard in the queue ruleset drifts = %v", drifts)
	}
	if last := findings[len(findings)-1]; !strings.Contains(last.Live, "refs/heads/lts-*") {
		t.Fatalf("the finding does not name the wildcard: %+v", last)
	}
}

// End to end against the ruleset API: with no queue ruleset a sync creates one on the default
// branch alone, named apart from the main ruleset (which it never touches).
func TestGitHubDriver_ReconcileMergeQueue_Positive_CreatesTheQueueRuleset(t *testing.T) {
	gh, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{{"id": 3, "name": RepositoryRulesetName}}})
	if _, err := gh.ReconcileMergeQueue(context.Background(), "main", &config.BranchProtectionPolicy{MergeQueue: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	create := fake.requests[1]
	if create.Method != http.MethodPost || !strings.Contains(create.Raw, `"name":"`+MergeQueueRulesetName+`"`) ||
		!strings.Contains(create.Raw, `"include":["refs/heads/main"]`) || strings.Contains(create.Raw, "*") {
		t.Fatalf("unexpected create request: %s %s", create.Method, create.Raw)
	}
	if types := ruleTypes(t, create); !slices.Equal(types, []string{MergeQueueRule}) {
		t.Fatalf("queue ruleset rules = %v", types)
	}
}

func TestGitHubDriver_ReconcileMergeQueue_Negative_NilPolicy(t *testing.T) {
	gh, _ := rulesetForge(t, &rulesetServer{})
	if _, err := gh.ReconcileMergeQueue(context.Background(), "main", nil); err == nil {
		t.Fatal("a nil policy reconciled")
	}
}

// A live queue ruleset that gained a wildcard keeps it through the union merge, with the
// operator's own parameter, and the live read reports it. A repository without one reads nil.
func TestGitHubDriver_ReconcileMergeQueue_Boundary_LiveWildcardSurvivesTheMerge(t *testing.T) {
	live := map[string]any{"id": 5, "name": MergeQueueRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main", "refs/heads/lts-*"}, "exclude": []any{}}},
		"rules":      []any{map[string]any{"type": MergeQueueRule, "parameters": map[string]any{"merge_method": "MERGE"}}}}
	gh, fake := rulesetForge(t, &rulesetServer{existing: []map[string]any{live}})
	if _, err := gh.ReconcileMergeQueue(context.Background(), "main", &config.BranchProtectionPolicy{MergeQueue: true}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if put := fake.requests[2]; put.Method != http.MethodPut || !strings.Contains(put.Raw, "refs/heads/lts-*") ||
		!strings.Contains(put.Raw, `"merge_method":"MERGE"`) {
		t.Fatalf("the union merge dropped the live include or parameter: %s %s", put.Method, put.Raw)
	}
	queue, err := gh.readQueueRuleset(context.Background(), map[int]string{5: MergeQueueRulesetName}, map[int]string{5: "active"})
	if err != nil || queue == nil || !slices.Equal(queue.Wildcards(), []string{"refs/heads/lts-*"}) {
		t.Fatalf("live read = %+v (%v), want the lts-* wildcard", queue, err)
	}
}

func TestGitHubDriver_ReadQueueRuleset_Boundary_NoneReadsNil(t *testing.T) {
	gh, _ := rulesetForge(t, &rulesetServer{})
	if none, err := gh.readQueueRuleset(context.Background(), map[int]string{3: RepositoryRulesetName}, nil); err != nil || none != nil {
		t.Fatalf("a repository without a queue ruleset read %+v (%v)", none, err)
	}
}
