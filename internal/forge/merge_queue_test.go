package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Three workflows: one that triggers on merge_group, one that runs on pull requests only and one
// that runs on a push only, so it requires no pull request check.
const (
	queueWorkflow   = "on:\n  pull_request:\n  merge_group:\njobs:\n  build:\n    name: Build\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n"
	noQueueWorkflow = "on:\n  pull_request:\njobs:\n  docs:\n    name: Documentation Governance\n    runs-on: ubuntu-latest\n    steps:\n      - run: make docs\n"
	pushOnlyFlow    = "on:\n  push:\n    branches: [main]\njobs:\n  scan:\n    name: Scan\n    runs-on: ubuntu-latest\n    steps:\n      - run: make scan\n"
)

func queueFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", queueWorkflow)
	writeWorkflowFixture(t, root, "docs.yml", noQueueWorkflow)
	writeWorkflowFixture(t, root, "scan.yml", pushOnlyFlow)
	return root
}

func queuedRuleTypes(t *testing.T, data []byte) []string {
	t.Helper()
	var doc struct {
		Rules []struct {
			Type string `json:"type"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(doc.Rules))
	for _, rule := range doc.Rules {
		types = append(types, rule.Type)
	}
	return types
}

// Positive: a queue-protected policy keeps the merge_queue rule out of the main ruleset and requires only the contexts
// of workflows that trigger on merge_group. Negative: the workflow without the trigger is
// excluded from the required checks and reported with its path and contexts. Boundary: a
// workflow that requires no pull request check is no finding.
func TestMergeQueueRendersOnlyMergeGroupContexts(t *testing.T) {
	root := queueFixture(t)
	policy := config.BranchProtectionPolicy{EnforceLinearHistory: true, RequiredApprovingReviewers: 1, MergeQueue: true}
	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(contexts, []string{"Build"}) {
		t.Fatalf("queue contexts = %v, want only Build", contexts)
	}
	if slices.Contains(queuedRuleTypes(t, data), "merge_queue") {
		t.Fatalf("the main ruleset carries the merge_queue rule, which lives in its own ruleset: %s", data)
	}
	if !strings.Contains(string(data), `"refs/heads/lts-*"`) {
		t.Fatalf("the main ruleset lost its lts-* include: %s", data)
	}
	requireDocsFinding(t, root)
}

// requireDocsFinding requires the findings of the queue fixture to be docs.yml alone.
func requireDocsFinding(t *testing.T, root string) {
	t.Helper()
	findings, err := MergeQueueFindings(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Workflow != ".github/workflows/docs.yml" ||
		!slices.Equal(findings[0].Contexts, []string{"Documentation Governance"}) || findings[0].Trigger() != "merge_group" {
		t.Fatalf("findings = %+v, want docs.yml alone", findings)
	}
	if !strings.Contains(findings[0].String(), "no merge_group trigger") {
		t.Fatalf("finding text = %q", findings[0])
	}
}

// Boundary: without a queue the rendering is the old one, byte for byte: no merge_queue rule,
// every pull request context, and no code-scanning rule without the default-setup declaration.
func TestNoMergeQueueRendersTheOldRuleset(t *testing.T) {
	root := queueFixture(t)
	policy := config.BranchProtectionPolicy{EnforceLinearHistory: true, RequiredApprovingReviewers: 1}
	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(contexts, []string{"Build", "Documentation Governance"}) {
		t.Fatalf("contexts = %v", contexts)
	}
	if slices.Contains(queuedRuleTypes(t, data), "merge_queue") || strings.Contains(string(data), "code_scanning") {
		t.Fatalf("a repository without a queue got a queue rule: %s", data)
	}
	again, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil || !bytes.Equal(data, again) {
		t.Fatalf("rendering is not stable: %v", err)
	}
}

// TestCodeScanningWithoutMergeQueue requires CodeQL scanning and preserves pull request contexts.
func TestCodeScanningWithoutMergeQueue(t *testing.T) {
	contexts := []string{"Build", "CodeQL", "Analyze (go)"}
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1, CodeQLDefaultSetup: true}
	data, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(queuedRuleTypes(t, data), "code_scanning") {
		t.Fatalf("code_scanning missing without a merge queue: %s", data)
	}
	for _, name := range contexts {
		if required, err := RulesetRequiresStatusContext(data, name); err != nil || !required {
			t.Fatalf("context %q required = %v, %v", name, required, err)
		}
	}
}

// Positive: CodeQL default setup, which no merge group runs, becomes a code_scanning rule and
// never a required status context. Negative: a queue without it renders no code_scanning rule.
func TestMergeQueueRendersCodeScanningForDefaultSetup(t *testing.T) {
	contexts := []string{"Build", "CodeQL / Analyze (go)", "Analyze (go)", "CodeQL"}
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1, MergeQueue: true, CodeQLDefaultSetup: true}
	data, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	types := queuedRuleTypes(t, data)
	if !slices.Contains(types, "code_scanning") || slices.Contains(types, "merge_queue") {
		t.Fatalf("rule types = %v", types)
	}
	for _, name := range []string{"CodeQL", "Analyze (go)"} {
		if required, err := RulesetRequiresStatusContext(data, name); err != nil || required {
			t.Fatalf("CodeQL context %q required = %v, %v", name, required, err)
		}
	}
	policy.CodeQLDefaultSetup = false
	data, err = RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil || slices.Contains(queuedRuleTypes(t, data), "code_scanning") {
		t.Fatalf("code_scanning without default setup: %s %v", data, err)
	}
}

// Boundary: the merge method follows the linear history requirement.
func TestQueueMergeMethod(t *testing.T) {
	if queueMergeMethod(true) != "SQUASH" || queueMergeMethod(false) != "MERGE" {
		t.Fatal("merge method does not follow linear history")
	}
}

// Positive and negative: the audit helper keeps only the findings whose context the branch
// requires, and narrows them to those contexts.
func TestRequiredWithoutMergeGroup(t *testing.T) {
	findings := []MergeQueueFinding{
		{Workflow: ".github/workflows/a.yml", Contexts: []string{"A1", "A2"}},
		{Workflow: ".github/workflows/b.yml", Contexts: []string{"B1"}},
	}
	got := RequiredWithoutMergeGroup(findings, []string{"A2", "other"})
	if len(got) != 1 || got[0].Workflow != ".github/workflows/a.yml" || !slices.Equal(got[0].Contexts, []string{"A2"}) {
		t.Fatalf("got %+v", got)
	}
	if got := RequiredWithoutMergeGroup(findings, nil); len(got) != 0 {
		t.Fatalf("nothing required, got %+v", got)
	}
	if MergeQueueFailure("x", nil) != "" {
		t.Fatal("no findings must be no failure")
	}
	text := MergeQueueFailure("The ruleset", findings[:1])
	for _, want := range []string{".github/workflows/a.yml", "merge_group", "A1, A2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure %q lacks %q", text, want)
		}
	}
}

// The queue ruleset is the one that carries the merge_queue rule.
func TestRulesetHasRuleReadsTheQueueRuleset(t *testing.T) {
	queue, err := RenderMergeQueueRuleset(FallbackDefaultBranch, config.BranchProtectionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	queueJSON, err := json.Marshal(queue)
	if err != nil {
		t.Fatal(err)
	}
	if has, err := RulesetHasRule(queueJSON, "merge_queue"); err != nil || !has {
		t.Fatalf("merge_queue rule of the queue ruleset = %v, %v", has, err)
	}
}

// The ruleset readers the audit uses: a queue rule and the required contexts are read, anything
// that is not one JSON document is refused.
func TestRulesetReadersForTheQueueAudit(t *testing.T) {
	data, err := RenderRepositoryRuleset(FallbackDefaultBranch, config.BranchProtectionPolicy{MergeQueue: true}, []string{"Build", "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	if has, err := RulesetHasRule(data, "merge_queue"); err != nil || has {
		t.Fatalf("the main ruleset carries a merge_queue rule = %v, %v", has, err)
	}

	if has, err := RulesetHasRule(data, "code_scanning"); err != nil || has {
		t.Fatalf("code_scanning rule = %v, %v", has, err)
	}
	if contexts, err := RulesetStatusContexts(data); err != nil || !slices.Equal(contexts, []string{"Build", "Docs"}) {
		t.Fatalf("contexts = %v, %v", contexts, err)
	}
	if _, err := RulesetHasRule([]byte("{"), "merge_queue"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, err := RulesetStatusContexts([]byte("{")); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

// Positive and negative: a live branch with an active merge_queue rule that requires a context
// of a workflow without merge_group is a finding; the same requirement without a queue rule, and
// a queue requiring only merge_group contexts, are not.
func TestLiveMergeQueueFindings(t *testing.T) {
	root := queueFixture(t)
	queue := LiveBranchRule{Type: "merge_queue"}
	checks := LiveBranchRule{Type: "required_status_checks", Parameters: json.RawMessage(
		`{"required_status_checks":[{"context":"Build"},{"context":"Documentation Governance"}]}`)}
	findings, err := LiveMergeQueueFindings(t.Context(), root, &LiveBranchProtection{Branch: "main", Rules: []LiveBranchRule{queue, checks}})
	if err != nil || len(findings) != 1 || findings[0].Workflow != ".github/workflows/docs.yml" {
		t.Fatalf("queue + docs context: %+v, %v", findings, err)
	}
	findings, err = LiveMergeQueueFindings(t.Context(), root, &LiveBranchProtection{Branch: "main", Rules: []LiveBranchRule{checks}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("no queue rule: %+v, %v", findings, err)
	}
	onlyBuild := LiveBranchRule{Type: "required_status_checks", Parameters: json.RawMessage(`{"required_status_checks":[{"context":"Build"}]}`)}
	findings, err = LiveMergeQueueFindings(t.Context(), root, &LiveBranchProtection{Branch: "main", Rules: []LiveBranchRule{queue, onlyBuild}})
	if err != nil || len(findings) != 0 {
		t.Fatalf("queue + merge_group context: %+v, %v", findings, err)
	}
	if findings, err := LiveMergeQueueFindings(t.Context(), root, nil); err != nil || findings != nil {
		t.Fatalf("nil live: %+v, %v", findings, err)
	}
}

// The option: the default selects every context, ForMergeQueue(false) the same, true only the
// merge_group ones.
func TestForMergeQueueOption(t *testing.T) {
	root := queueFixture(t)
	all, err := RequiredStatusContexts(t.Context(), root)
	if err != nil || len(all) != 2 {
		t.Fatalf("default = %v, %v", all, err)
	}
	queued, err := RequiredStatusContexts(t.Context(), root, ForMergeQueue(true))
	if err != nil || !slices.Equal(queued, []string{"Build"}) {
		t.Fatalf("queued = %v, %v", queued, err)
	}
	off, err := RequiredStatusContexts(t.Context(), root, ForMergeQueue(false))
	if err != nil || !slices.Equal(off, all) {
		t.Fatalf("off = %v, %v", off, err)
	}
	var noContext context.Context
	if _, err := MergeQueueFindings(noContext, root, nil); err == nil {
		t.Fatal("nil context accepted")
	}
}

// TestCodeScanningPriorAcceptsEarlierNonQueueRendering checks the old rendering remains refreshable.
func TestCodeScanningPriorAcceptsEarlierNonQueueRendering(t *testing.T) {
	policy := config.BranchProtectionPolicy{RequiredApprovingReviewers: 1}
	old, err := RenderRepositoryRuleset("main", policy, []string{"CodeQL"})
	if err != nil {
		t.Fatal(err)
	}
	policy.CodeQLDefaultSetup = true
	current, err := RenderRepositoryRuleset("main", policy, []string{"CodeQL"})
	if err != nil {
		t.Fatal(err)
	}
	if _, known, _ := util.LookupCanonicalText(old, PriorRulesetDigests(RulesetBaseline{Branch: "main", Policy: policy, Contexts: []string{"CodeQL"}}, current)); !known {
		t.Fatalf("earlier rendering not recognised: %s", old)
	}
}

// TestCodeScanningMergeKeepsLiveContexts verifies non-queue reconciliation preserves live CodeQL checks.
func TestCodeScanningMergeKeepsLiveContexts(t *testing.T) {
	policy := config.BranchProtectionPolicy{CodeQLDefaultSetup: true}
	data, err := RenderRepositoryRuleset("main", policy, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := jsonObject(data)
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]any{"rules": []any{map[string]any{"type": "required_status_checks", "parameters": map[string]any{
		"required_status_checks": []any{map[string]any{"context": "CodeQL"}},
	}}}}
	merged, _, _, err := mergeRuleset(live, desired, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if required, err := RulesetRequiresStatusContext(encoded, "CodeQL"); err != nil || !required {
		t.Fatalf("live CodeQL context required = %v, %v", required, err)
	}
}

// TestCodeScanningPriorAcceptsExistingSetupWhenQueueAdded preserves the current non-queue rendering.
func TestCodeScanningPriorAcceptsExistingSetupWhenQueueAdded(t *testing.T) {
	policy := config.BranchProtectionPolicy{CodeQLDefaultSetup: true}
	old, err := RenderRepositoryRuleset("main", policy, []string{"Build", "CodeQL"})
	if err != nil {
		t.Fatal(err)
	}
	policy.MergeQueue = true
	current, err := RenderRepositoryRuleset("main", policy, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	baseline := RulesetBaseline{Branch: "main", Policy: policy, Contexts: []string{"Build"}, UnqueuedContexts: []string{"Build", "CodeQL"}}
	if _, known, _ := util.LookupCanonicalText(old, PriorRulesetDigests(baseline, current)); !known {
		t.Fatal("the prior CodeQL rendering was not recognised when adding a queue")
	}
}

// TestCodeScanningForbiddenIncludesMessage preserves the forge's explanation of a denied read.
func TestCodeScanningForbiddenIncludesMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		if _, err := w.Write([]byte(`{"message":"secondary rate limit"}`)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	driver := NewGitHubDriver("stub-token", server.URL)
	driver.SetRepository("acme", "widgets")
	_, err := driver.CodeQLDefaultSetupState(t.Context())
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "secondary rate limit") {
		t.Fatalf("error = %v, want HTTP 403 and response message", err)
	}
}

// TestCodeScanningPriorQueueRemovedAndSetupDeclared preserves the earlier queued rendering.
func TestCodeScanningPriorQueueRemovedAndSetupDeclared(t *testing.T) {
	policy := config.BranchProtectionPolicy{EnforceLinearHistory: true, RequiredApprovingReviewers: 1, CodeQLDefaultSetup: true}
	queued := policy
	queued.MergeQueue, queued.CodeQLDefaultSetup = true, false
	onDisk, err := RenderRepositoryRuleset("main", queued, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := RenderRepositoryRuleset("main", policy, []string{"Build", "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	baseline := RulesetBaseline{Branch: "main", Policy: policy, Contexts: []string{"Build", "Docs"}, QueuedContexts: []string{"Build"}, queuedRead: true}
	priors := PriorRulesetDigests(baseline, current)
	if _, known, _ := util.LookupCanonicalText(onDisk, priors); !known {
		t.Error("earlier queued rendering is not a prior")
	}
	queued.RequiredApprovingReviewers = 3
	edited, err := RenderRepositoryRuleset("main", queued, []string{"Build"})
	if err != nil {
		t.Fatal(err)
	}
	if _, known, _ := util.LookupCanonicalText(edited, priors); known {
		t.Error("edited rendering matched a prior")
	}
}
