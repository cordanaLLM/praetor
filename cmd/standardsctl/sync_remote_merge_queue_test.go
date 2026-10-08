// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

const (
	queuedUnitWorkflow = "on:\n  pull_request:\n  merge_group:\njobs:\n  unit:\n    name: Unit Tests\n    runs-on: ubuntu-latest\n"
	plainDocsWorkflow  = "on: pull_request\njobs:\n  docs:\n    name: Docs Check\n    runs-on: ubuntu-latest\n"
)

// queueRemoteFixture is a sync fixture for acme/widgets with one workflow that triggers on
// merge_group and one that does not. With declared, the manifest declares
// overrides.branch_protection.merge_queue and the committed ruleset is the queue rendering.
func queueRemoteFixture(t *testing.T, declared bool) *auditFixture {
	t.Helper()
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", queuedUnitWorkflow)
	writeFixtureFile(t, f.dir, ".github/workflows/docs.yml", plainDocsWorkflow)
	policy := config.DefaultPolicy().BranchProtection
	policy.MergeQueue = declared
	if declared {
		writeFixtureFile(t, f.dir, ".standards.yaml", strings.Replace(fixtureManifest("acme", "widgets", false),
			"register:\n", "overrides:\n  branch_protection:\n    merge_queue: true\nregister:\n", 1))
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), f.dir, forge.ForMergeQueue(declared))
	if err != nil {
		t.Fatalf("RequiredStatusContexts: %v", err)
	}
	if err := synthesizeRuleset(f.dir, "main", policy, contexts); err != nil {
		t.Fatal(err)
	}
	env := initGitFixture(t, f.dir)
	if out, err := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("remote add: %v (%s)", err, out)
	}
	return f
}

// liveMainRuleset is the live main ruleset (id 1): the default branch and lts-*, requiring the
// named contexts.
func liveMainRuleset(contexts ...string) map[string]any {
	rules := []any{map[string]any{"type": "deletion"}}
	if len(contexts) > 0 {
		checks := make([]any, 0, len(contexts))
		for _, name := range contexts {
			checks = append(checks, map[string]any{"context": name})
		}
		rules = append(rules, map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": true, "required_status_checks": checks,
		}})
	}
	return map[string]any{
		"id": 1, "name": forge.RepositoryRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main", "refs/heads/lts-*"}, "exclude": []any{}}},
		"rules":      rules,
	}
}

// liveQueueRuleset is the live merge queue ruleset (id 2) over includes, with an operator's own
// queue parameters.
func liveQueueRuleset(includes ...string) map[string]any {
	refs := make([]any, 0, len(includes))
	for _, ref := range includes {
		refs = append(refs, ref)
	}
	return map[string]any{
		"id": 2, "name": forge.MergeQueueRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": refs, "exclude": []any{}}},
		"rules": []any{map[string]any{"type": forge.MergeQueueRule, "parameters": map[string]any{
			"check_response_timeout_minutes": 15, "grouping_strategy": "ALLGREEN", "max_entries_to_build": 3,
			"max_entries_to_merge": 3, "merge_method": "SQUASH", "min_entries_to_merge": 1,
			"min_entries_to_merge_wait_minutes": 1,
		}}},
	}
}

// ruleTypes returns the rule types of a stored ruleset, in order.
func ruleTypes(t *testing.T, stored string) []string {
	t.Helper()
	var doc struct {
		Rules []struct {
			Type string `json:"type"`
		} `json:"rules"`
	}
	if err := json.Unmarshal([]byte(stored), &doc); err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(doc.Rules))
	for _, rule := range doc.Rules {
		types = append(types, rule.Type)
	}
	return types
}

// Positive: a declared queue lands in its own ruleset that includes the default branch alone and
// carries the merge_queue rule alone, while the live main ruleset keeps main and lts-* and gains
// no merge_queue rule. The checks are those of the merge_group workflows, the omitted workflow is
// printed once without guard wording, and the output names both rulesets truthfully.
func TestSync_Remote_DeclaredMergeQueueGetsItsOwnRuleset(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset()}}
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, true), stub)
	queue := stub.storedRuleset(t, 2)
	mustContain(t, remote, `"include":["refs/heads/main","refs/heads/lts-*"]`)
	mustContain(t, queue, `"name":"`+forge.MergeQueueRulesetName+`"`, `"include":["refs/heads/main"]`)
	if strings.Contains(queue, "lts-*") || strings.Contains(queue, "*") {
		t.Errorf("the queue ruleset holds a wildcard: %s", queue)
	}
	if got := ruleTypes(t, queue); len(got) != 1 || got[0] != forge.MergeQueueRule {
		t.Errorf("queue ruleset rules = %v, want only merge_queue", got)
	}
	if slices.Contains(ruleTypes(t, remote), forge.MergeQueueRule) {
		t.Errorf("the main ruleset carries a merge_queue rule: %s", remote)
	}
	requireRulesetCheck(t, "main GitHub ruleset", remote, "Unit Tests", true)
	requireRulesetCheck(t, "main GitHub ruleset", remote, "Docs Check", false)
	mustContain(t, out, "[WARN] .github/workflows/docs.yml: not required by the merge queue ruleset", "merge_group:", "Docs Check",
		"[OK] Remote branch protection synchronized on GitHub (main and lts-*, read back",
		`[OK] Merge queue ruleset "`+forge.MergeQueueRulesetName+`" synchronized on GitHub (main only, read back)`)
	if n := strings.Count(out, "not required by the merge queue ruleset"); n != 1 {
		t.Errorf("the queue omission was printed %d times, want once:\n%s", n, out)
	}
	if strings.Contains(out, "repository guard") || strings.Contains(out, "(main only, read back; live rules") {
		t.Errorf("queue omission worded as a guard, or the main ruleset reported as main only:\n%s", out)
	}
}

// Positive: a live queue ruleset keeps the operator's own queue parameters through a sync, and
// the sync lowers nothing. Boundary: a second sync of the converged forge writes the same
// rulesets and still keeps them.
func TestSync_Remote_DeclaredMergeQueueKeepsLiveQueueParameters(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset(), 2: liveQueueRuleset("refs/heads/main")}}
	out, _, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, true), stub)
	queue := stub.storedRuleset(t, 2)
	mustContain(t, queue, `"check_response_timeout_minutes":15`, `"include":["refs/heads/main"]`)
	if strings.Contains(out, "[LOWERED]") {
		t.Errorf("a queue parameter was lowered:\n%s", out)
	}
	for _, write := range stub.recorded() {
		if strings.HasPrefix(write, "POST ") && strings.HasSuffix(write, "/rulesets") {
			t.Errorf("sync created a ruleset beside the live ones: %v", stub.recorded())
		}
	}
}

// Negative: a merge queue only the forge carries does not switch the selection: the declared key
// alone does, so sync --remote requires both workflows' checks, leaves the queue ruleset as it is
// (no write to it) and tells the operator how to resolve it.
func TestSync_Remote_UndeclaredLiveMergeQueueRulesetIsReportedNotWritten(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset(), 2: liveQueueRuleset("refs/heads/main")}}
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, false), stub)
	requireRulesetCheck(t, "plain GitHub ruleset", remote, "Docs Check", true)
	requireRulesetCheck(t, "plain GitHub ruleset", remote, "Unit Tests", true)
	mustContain(t, out, `[WARN] ruleset "`+forge.MergeQueueRulesetName+`" still exists on GitHub while the policy declares no merge queue`)
	if strings.Contains(out, "not required by the merge queue ruleset") {
		t.Errorf("sync warned about omissions of a queue nobody declared:\n%s", out)
	}
	for _, write := range stub.recorded() {
		if strings.HasSuffix(write, "/rulesets/2") {
			t.Errorf("sync wrote the undeclared queue ruleset: %s", write)
		}
	}
}

// Positive: a declared queue whose live main ruleset still requires a check of a workflow without
// merge_group (written by an earlier sync, kept by the union merge) gets a queue-specific warning
// naming it, never "synchronized", and no guard wording.
func TestSync_Remote_DeclaredMergeQueueWarnsAboutAStaleCheck(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset("Docs Check")}}
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, true), stub)
	requireRulesetCheck(t, "stale GitHub ruleset", remote, "Docs Check", true)
	mustContain(t, out, "[WARN] Remote branch protection written, but main still requires checks the merge queue never receives",
		".github/workflows/docs.yml: no merge_group trigger")
	if strings.Contains(out, "[OK] Remote branch protection synchronized") || strings.Contains(out, "repository guard") {
		t.Errorf("a stale queue check was reported as synchronized or as a guard omission:\n%s", out)
	}
}

// Positive: plan --remote previews exactly what sync --remote writes for both rulesets. Before
// the sync it reports the missing merge queue as drift and writes nothing; after the sync it
// reports no drift. Negative: a queue ruleset that gained a wildcard is drift in the preview and
// sync, which keeps the live include (union merge), fails its readback instead of reporting it
// as synchronized.
func TestPlanRemote_PreviewsWhatSyncWritesForTheQueueRuleset(t *testing.T) {
	stub := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset()}}
	f := queueRemoteFixture(t, true)
	srv := httptest.NewServer(stub.handler())
	t.Cleanup(srv.Close)
	args := []string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + srv.URL}

	before, err := runPlanCmd(t, args...)
	if err != nil {
		t.Fatalf("plan --remote: %v\n%s", err, before)
	}
	mustContain(t, before, "[DRIFT] Merge queue: declared required, live not enforced", "[DRIFT] GitHub does not enforce the declared")
	if writes := stub.recorded(); len(writes) != 0 {
		t.Fatalf("plan --remote wrote: %v", writes)
	}
	if out, err := runSyncCmd(t, args...); err != nil {
		t.Fatalf("sync --remote: %v\n%s", err, out)
	}
	after, err := runPlanCmd(t, args...)
	if err != nil {
		t.Fatalf("plan --remote after sync: %v\n%s", err, after)
	}
	mustContain(t, after, "[OK] Merge queue: declared required, live required by ruleset \""+forge.MergeQueueRulesetName+"\" #2",
		"Status: GitHub enforces every declared branch protection property.")

	wild := &forgeStub{rulesets: map[int]map[string]any{1: liveMainRuleset(), 2: liveQueueRuleset("refs/heads/main", "refs/heads/lts-*")}}
	wildSrv := httptest.NewServer(wild.handler())
	t.Cleanup(wildSrv.Close)
	wildArgs := []string{"--config=" + f.manifestPath, "--remote", "--token=ghp_x", "--endpoint=" + wildSrv.URL}
	preview, err := runPlanCmd(t, wildArgs...)
	if err != nil {
		t.Fatalf("plan --remote on a wildcard queue ruleset: %v\n%s", err, preview)
	}
	mustContain(t, preview, "[DRIFT] Merge queue ruleset refs", "wildcard refs/heads/lts-*")
	_, err = runSyncCmd(t, wildArgs...)
	mustErrContain(t, err, "Merge queue ruleset refs")
}
