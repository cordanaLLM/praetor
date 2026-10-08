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
// merge_group and one that does not, and no merge_queue declared in the manifest.
func queueRemoteFixture(t *testing.T) *auditFixture {
	t.Helper()
	f := newSyncValidationFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/ci.yml", queuedUnitWorkflow)
	writeFixtureFile(t, f.dir, ".github/workflows/docs.yml", plainDocsWorkflow)
	contexts, err := forge.RequiredStatusContexts(t.Context(), f.dir)
	if err != nil {
		t.Fatalf("RequiredStatusContexts: %v", err)
	}
	if err := synthesizeRuleset(f.dir, "main", config.DefaultPolicy().BranchProtection, contexts); err != nil {
		t.Fatal(err)
	}
	env := initGitFixture(t, f.dir)
	if out, err := runFixtureGit(t, f.dir, env, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatalf("remote add: %v (%s)", err, out)
	}
	return f
}

// liveQueueRuleset is a live praetor ruleset that carries a merge_queue rule and requires nothing,
// the queue an operator configured on GitHub that .standards.yaml does not declare.
func liveQueueRuleset() map[string]any {
	return map[string]any{
		"id": 1, "name": forge.RepositoryRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/heads/main"}, "exclude": []any{}}},
		"rules": []any{map[string]any{"type": "merge_queue", "parameters": map[string]any{
			"check_response_timeout_minutes": 15, "grouping_strategy": "ALLGREEN", "max_entries_to_build": 3,
			"max_entries_to_merge": 3, "merge_method": "SQUASH", "min_entries_to_merge": 1,
			"min_entries_to_merge_wait_minutes": 1,
		}}},
	}
}

// Positive: a merge queue only the forge carries makes sync --remote require the checks of the
// merge_group workflows alone, warn about the workflow it left out, and keep the operator's queue
// parameters. Negative: without a live queue, both workflows' checks are required and nothing is
// warned about.
func TestSync_Remote_LiveMergeQueueSelectsMergeGroupChecks(t *testing.T) {
	out, remote, _ := runGuardedRemoteSync(t, queueRemoteFixture(t), &forgeStub{rulesets: map[int]map[string]any{1: liveQueueRuleset()}})
	requireRulesetCheck(t, "queued GitHub ruleset", remote, "Unit Tests", true)
	requireRulesetCheck(t, "queued GitHub ruleset", remote, "Docs Check", false)
	mustContain(t, out, "[WARN] .github/workflows/docs.yml: not required by the merge queue ruleset", "merge_group:", "Docs Check")
	mustContain(t, remote, `"check_response_timeout_minutes":15`)

	out, remote, _ = runGuardedRemoteSync(t, queueRemoteFixture(t), &forgeStub{})
	requireRulesetCheck(t, "plain GitHub ruleset", remote, "Docs Check", true)
	if strings.Contains(out, "not required by the merge queue ruleset") {
		t.Errorf("sync warned about a queue nobody declared:\n%s", out)
	}
}
