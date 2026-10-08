// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: a queue names every workflow it leaves out of the required checks, with the path, the
// trigger to add and its contexts. Negative: without a queue nothing is named. Boundary: a
// workflow that requires no pull request check is no omission.
func TestQueueOmissions(t *testing.T) {
	root := queueFixture(t)
	lines, err := QueueOmissions(t.Context(), root, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], ".github/workflows/docs.yml") ||
		!strings.Contains(lines[0], "merge_group:") || !strings.Contains(lines[0], "Documentation Governance") {
		t.Fatalf("omissions = %q, want docs.yml alone, with its trigger and context", lines)
	}
	if lines, err = QueueOmissions(t.Context(), root, nil, false); err != nil || lines != nil {
		t.Fatalf("no queue must name nothing, got %q, %v", lines, err)
	}
	planned := map[string][]byte{".github/workflows/docs.yml": []byte(strings.Replace(noQueueWorkflow, "pull_request:", "pull_request:\n  merge_group:", 1))}
	if lines, err = QueueOmissions(t.Context(), root, planned, true); err != nil || len(lines) != 0 {
		t.Fatalf("a planned merge_group trigger must clear the omission, got %q, %v", lines, err)
	}
}

func queueDefaults(t *testing.T) map[string]any {
	t.Helper()
	params, ok := mergeQueueRuleOf(true)["parameters"].(map[string]any)
	if !ok {
		t.Fatal("the rendered merge_queue rule has no parameters")
	}
	return params
}

func queueRuleOf(params map[string]any) map[string]any {
	return map[string]any{"type": "merge_queue", "parameters": params}
}

func mergedRuleParams(t *testing.T, merged map[string]any, ruleType string) map[string]any {
	t.Helper()
	rules, ok := merged["rules"].([]any)
	if !ok {
		t.Fatalf("merged ruleset has no rules: %v", merged)
	}
	for _, rule := range rules {
		if object, ok := rule.(map[string]any); ok && object["type"] == ruleType {
			params, isMap := object["parameters"].(map[string]any)
			if !isMap {
				t.Fatalf("rule %s has no parameters: %v", ruleType, object)
			}
			return params
		}
	}
	t.Fatalf("merged ruleset has no %s rule: %v", ruleType, merged)
	return nil
}

// Positive: a live merge_queue keeps the operator's own parameters through a sync, and a
// parameter the live rule lacks takes the rendered default. Boundary: with no live queue rule the
// rendered rule is written whole.
func TestMergeRulesetKeepsLiveMergeQueueParameters(t *testing.T) {
	desired := map[string]any{"name": "n", "target": "branch", "enforcement": "active",
		"rules": []any{queueRuleOf(queueDefaults(t))}}
	live := map[string]any{"rules": []any{queueRuleOf(map[string]any{
		"check_response_timeout_minutes": 15, "merge_method": "REBASE", "max_entries_to_build": 2,
	})}}
	merged, lowered, err := mergeRuleset(live, desired)
	if err != nil {
		t.Fatal(err)
	}
	params := mergedRuleParams(t, merged, "merge_queue")
	if params["check_response_timeout_minutes"] != 15 || params["merge_method"] != "REBASE" || params["max_entries_to_build"] != 2 {
		t.Fatalf("live queue parameters were overwritten: %v", params)
	}
	if params["grouping_strategy"] != "ALLGREEN" || params["min_entries_to_merge"] != queueMinEntriesToMerge {
		t.Fatalf("a missing parameter must take the rendered default: %v", params)
	}
	if len(lowered) != 0 {
		t.Fatalf("lowered = %v, want none", lowered)
	}
	fresh, _, err := mergeRuleset(map[string]any{"rules": []any{}}, desired)
	if err != nil {
		t.Fatal(err)
	}
	if got := mergedRuleParams(t, fresh, "merge_queue"); got["merge_method"] != "SQUASH" {
		t.Fatalf("no live queue rule must write the rendered one: %v", got)
	}
}

// mergedTools returns the code_scanning tool entries of a merged ruleset.
func mergedTools(t *testing.T, merged map[string]any) []map[string]any {
	t.Helper()
	raw, ok := mergedRuleParams(t, merged, "code_scanning")["code_scanning_tools"].([]any)
	if !ok {
		t.Fatalf("code_scanning lists no tools: %v", merged)
	}
	tools := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		tool, isMap := entry.(map[string]any)
		if !isMap {
			t.Fatalf("tool entry %v is not an object", entry)
		}
		tools = append(tools, tool)
	}
	return tools
}

func scanningTool(tool, errorsThreshold string) map[string]any {
	return map[string]any{"tool": tool, "alerts_threshold": errorsThreshold, "security_alerts_threshold": "critical"}
}

// Positive: a live code_scanning keeps every tool it lists with its thresholds, and CodeQL is
// appended only when absent. Negative: a live CodeQL entry with a stricter threshold is not
// replaced by the rendered one.
func TestMergeRulesetUnionsLiveCodeScanningTools(t *testing.T) {
	desired := map[string]any{"name": "n", "target": "branch", "enforcement": "active",
		"rules": []any{map[string]any{"type": "code_scanning", "parameters": map[string]any{
			"code_scanning_tools": []any{scanningTool("CodeQL", "errors")}}}}}
	other := map[string]any{"rules": []any{map[string]any{"type": "code_scanning", "parameters": map[string]any{
		"code_scanning_tools": []any{scanningTool("Semgrep", "all")}}}}}
	merged, _, err := mergeRuleset(other, desired)
	if err != nil {
		t.Fatal(err)
	}
	tools := mergedTools(t, merged)
	if len(tools) != 2 || tools[0]["tool"] != "Semgrep" || tools[1]["tool"] != "CodeQL" {
		t.Fatalf("tools = %v, want the live Semgrep kept and CodeQL appended", tools)
	}
	strict := map[string]any{"rules": []any{map[string]any{"type": "code_scanning", "parameters": map[string]any{
		"code_scanning_tools": []any{scanningTool("CodeQL", "all")}}}}}
	merged, _, err = mergeRuleset(strict, desired)
	if err != nil {
		t.Fatal(err)
	}
	tools = mergedTools(t, merged)
	if len(tools) != 1 || tools[0]["alerts_threshold"] != "all" {
		t.Fatalf("tools = %v, want the live CodeQL threshold kept", tools)
	}
}

// Positive: the ruleset a repository carried before it declared a merge queue is Praetor's, so
// adoption and flavor apply refresh it to the queue rendering without --force. Negative: that
// rendering is no prior when the policy declares no queue , and an edited queue-less ruleset still matches nothing.
func TestPriorRulesetDigestsAcceptTheQueuelessRendering(t *testing.T) {
	policy := config.BranchProtectionPolicy{EnforceLinearHistory: true, RequiredApprovingReviewers: 1}
	all := []string{"Build", "Documentation Governance"}
	queueless, err := RenderRepositoryRuleset("main", policy, all)
	if err != nil {
		t.Fatal(err)
	}
	queued := policy
	queued.MergeQueue = true
	current, err := RenderRepositoryRuleset("main", queued, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	baseline := RulesetBaseline{Branch: "main", Policy: queued, Contexts: []string{"Build"}, UnqueuedContexts: all}
	if _, known, _ := util.LookupCanonicalText(queueless, PriorRulesetDigests(baseline, current)); !known {
		t.Fatalf("the queue-less rendering is not a prior:\n%s", queueless)
	}
	edited := policy
	edited.RequiredApprovingReviewers = 3
	editedData, err := RenderRepositoryRuleset("main", edited, all)
	if err != nil {
		t.Fatal(err)
	}
	if _, known, _ := util.LookupCanonicalText(editedData, PriorRulesetDigests(baseline, current)); known {
		t.Fatal("an edited queue-less ruleset must keep the --force contract")
	}
	plain := RulesetBaseline{Branch: "main", Policy: policy, Contexts: all}
	if _, known, _ := util.LookupCanonicalText(queueless, PriorRulesetDigests(plain, current)); !known {
		t.Fatal("a baseline without a queue still yields its own rendering")
	}
}

// ReadRulesetBaseline reads the queue-less contexts only for a policy that declares a queue.
func TestReadRulesetBaselineQueuelessContexts(t *testing.T) {
	root := queueFixture(t)
	writeManifestFixture(t, root, "overrides:\n  branch_protection:\n    merge_queue: true\n")
	baseline, err := ReadRulesetBaseline(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(baseline.Contexts, []string{"Build"}) ||
		!slices.Equal(baseline.UnqueuedContexts, []string{"Build", "Documentation Governance"}) {
		t.Fatalf("contexts %v, queue-less contexts %v", baseline.Contexts, baseline.UnqueuedContexts)
	}
	plainRoot := queueFixture(t)
	if baseline, err = ReadRulesetBaseline(t.Context(), plainRoot); err != nil || baseline.UnqueuedContexts != nil {
		t.Fatalf("no queue must read no queue-less contexts: %v %v", baseline.UnqueuedContexts, err)
	}
}

// Positive: removing the queue declaration leaves the queue rendering on disk, which is Praetor's
// too, so the run that removes it refreshes the file without --force. Negative: an edited queue
// rendering matches nothing, and a baseline that read no queued contexts (queuedRead false) yields no queue prior.
func TestPriorRulesetDigestsAcceptTheQueueRendering(t *testing.T) {
	policy := config.BranchProtectionPolicy{EnforceLinearHistory: true, RequiredApprovingReviewers: 1}
	all := []string{"Build", "Documentation Governance"}
	queued := policy
	queued.MergeQueue = true
	onDisk, err := RenderRepositoryRuleset("main", queued, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := RenderRepositoryRuleset("main", policy, all)
	if err != nil {
		t.Fatal(err)
	}
	baseline := RulesetBaseline{Branch: "main", Policy: policy, Contexts: all, QueuedContexts: []string{"Build"}, queuedRead: true}
	if _, known, _ := util.LookupCanonicalText(onDisk, PriorRulesetDigests(baseline, current)); !known {
		t.Fatalf("the queue rendering is not a prior of the queue-less one:\n%s", onDisk)
	}
	edited := queued
	edited.RequiredApprovingReviewers = 3
	editedData, err := RenderRepositoryRuleset("main", edited, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	if _, known, _ := util.LookupCanonicalText(editedData, PriorRulesetDigests(baseline, current)); known {
		t.Fatal("an edited queue ruleset must keep the --force contract")
	}
	baseline.queuedRead = false
	if _, known, _ := util.LookupCanonicalText(onDisk, PriorRulesetDigests(baseline, current)); known {
		t.Fatal("a baseline without queued contexts must not accept the queue rendering")
	}
}

// ReadRulesetBaseline reads the merge_group contexts for a policy without a queue.
func TestReadRulesetBaselineQueuedContexts(t *testing.T) {
	baseline, err := ReadRulesetBaseline(t.Context(), queueFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(baseline.QueuedContexts, []string{"Build"}) {
		t.Fatalf("queued contexts = %v", baseline.QueuedContexts)
	}
}

// Positive: with CodeQL default setup under a queue, a CodeQL context handed to the renderer is
// dropped, and so is one a live ruleset requires and the union merge would keep. Negative: the
// same contexts stay without a queue, and an ordinary live context is kept.
func TestMergeQueueDropsCodeQLDefaultSetupContexts(t *testing.T) {
	contexts := []string{"Build", "CodeQL / Analyze (go)", "Analyze (go)", "CodeQL"}
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1, MergeQueue: true, CodeQLDefaultSetup: true}
	data, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RulesetStatusContexts(data)
	if err != nil || !slices.Equal(got, []string{"Build"}) {
		t.Fatalf("queue contexts = %v, %v", got, err)
	}
	policy.MergeQueue = false
	data, err = RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if got, err = RulesetStatusContexts(data); err != nil || !slices.Equal(got, contexts) {
		t.Fatalf("queue-less contexts = %v, %v", got, err)
	}

	policy.MergeQueue = true
	desiredData, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := jsonObject(desiredData)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]any{"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
		"required_status_checks": []any{
			map[string]any{"context": "CodeQL / Analyze (go)"}, map[string]any{"context": "Legacy Gate"},
		},
	}}}}
	merged, _, err := mergeRuleset(live, desired)
	if err != nil {
		t.Fatal(err)
	}
	checks, ok := mergedRuleParams(t, merged, "required_status_checks")["required_status_checks"].([]any)
	if !ok {
		t.Fatal("merged ruleset has no required status checks")
	}
	var names []string
	for _, check := range checks {
		entry, isObject := check.(map[string]any)
		name, isString := entry["context"].(string)
		if !isObject || !isString {
			t.Fatalf("malformed merged check %v", check)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Build", "Legacy Gate"}) {
		t.Fatalf("merged contexts = %v, want the live CodeQL context dropped and Legacy Gate kept", names)
	}
}
