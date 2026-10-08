// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
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

// liveQueueRuleset is a live praetor ruleset on main that carries a merge_queue rule and requires
// the named contexts.
func liveQueueRuleset(contexts ...string) map[string]any {
	checks := make([]any, 0, len(contexts))
	for _, name := range contexts {
		checks = append(checks, map[string]any{"context": name})
	}
	rules := []any{map[string]any{"type": forge.MergeQueueRule, "parameters": map[string]any{
		"check_response_timeout_minutes": 15, "grouping_strategy": "ALLGREEN", "max_entries_to_build": 3,
		"max_entries_to_merge": 3, "merge_method": "SQUASH", "min_entries_to_merge": 1,
		"min_entries_to_merge_wait_minutes": 1,
	}}}
	if len(checks) > 0 {
		rules = append(rules, map[string]any{"type": "required_status_checks", "parameters": map[string]any{
			"strict_required_status_checks_policy": true, "required_status_checks": checks,
		}})
	}
	return map[string]any{
		"id": 1, "name": forge.RepositoryRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules":      rules,
	}
}

// Positive: a declared queue makes sync --remote require the checks of the merge_group workflows
// alone, keep the operator's live queue parameters, target the default branch alone, print the
// omitted workflow once, and report the ruleset as synchronized. Negative: that omission is not a
// repository guard (no guard wording).
func TestSync_Remote_DeclaredMergeQueueSelectsMergeGroupChecks(t *testing.T) {
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, true), &forgeStub{rulesets: map[int]map[string]any{1: liveQueueRuleset()}})
	requireRulesetCheck(t, "queued GitHub ruleset", remote, "Unit Tests", true)
	requireRulesetCheck(t, "queued GitHub ruleset", remote, "Docs Check", false)
	mustContain(t, out, "[WARN] .github/workflows/docs.yml: not required by the merge queue ruleset", "merge_group:", "Docs Check",
		"[OK] Remote branch protection synchronized on GitHub (main only, read back")
	mustContain(t, remote, `"check_response_timeout_minutes":15`)
	if n := strings.Count(out, "not required by the merge queue ruleset"); n != 1 {
		t.Errorf("the queue omission was printed %d times, want once:\n%s", n, out)
	}
	if strings.Contains(out, "repository guard") || strings.Contains(remote, "lts-*") {
		t.Errorf("queue omission worded as a guard or the ruleset kept a wildcard include:\n%s\n%s", out, remote)
	}
}

// Negative: a merge queue only the forge carries does not switch the selection: the declared key
// alone does, so sync --remote requires both workflows' checks (what plan --remote previews) and
// says nothing about a queue.
func TestSync_Remote_UndeclaredLiveMergeQueueDoesNotSelectChecks(t *testing.T) {
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, false), &forgeStub{rulesets: map[int]map[string]any{1: liveQueueRuleset()}})
	requireRulesetCheck(t, "plain GitHub ruleset", remote, "Docs Check", true)
	requireRulesetCheck(t, "plain GitHub ruleset", remote, "Unit Tests", true)
	if strings.Contains(out, "not required by the merge queue ruleset") {
		t.Errorf("sync warned about a queue nobody declared:\n%s", out)
	}
}

// Positive: a declared queue whose live ruleset still requires a check of a workflow without
// merge_group (written by an earlier sync, kept by the union merge) gets a queue-specific warning
// naming it, never "synchronized", and no guard wording. Boundary: the same declaration without
// the stale check reports [OK] (the test above).
func TestSync_Remote_DeclaredMergeQueueWarnsAboutAStaleCheck(t *testing.T) {
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t, true),
		&forgeStub{rulesets: map[int]map[string]any{1: liveQueueRuleset("Docs Check")}})
	requireRulesetCheck(t, "stale GitHub ruleset", remote, "Docs Check", true)
	mustContain(t, out, "[WARN] Remote branch protection written, but main still requires checks the merge queue never receives",
		".github/workflows/docs.yml: no merge_group trigger")
	if strings.Contains(out, "[OK] Remote branch protection synchronized") || strings.Contains(out, "repository guard") {
		t.Errorf("a stale queue check was reported as synchronized or as a guard omission:\n%s", out)
	}
}
