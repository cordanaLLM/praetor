// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// A repository's rendering is RenderRepositoryRuleset over the contexts its workflows name, the
// file ValidateRepositoryRuleset then accepts.
func TestRenderRulesetForRepository_Positive_ChecksComeFromTheWorkflows(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	policy := config.DefaultPolicy().BranchProtection

	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !slices.Equal(contexts, []string{"verify"}) {
		t.Fatalf("contexts = %v, want [verify]", contexts)
	}
	want, err := RenderRepositoryRuleset(policy, contexts)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("rendering differs from RenderRepositoryRuleset: %v\n%s", err, data)
	}
	if err := ValidateRepositoryRuleset(data, policy, contexts); err != nil {
		t.Fatalf("the rendering does not validate: %v", err)
	}
}

// A workflow inventory that cannot be read, an invalid policy and a cancelled context render
// nothing.
func TestRenderRulesetForRepository_Negative_RefusesWhatItCannotRender(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "jobs: [")
	if data, _, err := RenderRulesetForRepository(t.Context(), root, config.DefaultPolicy().BranchProtection, nil); err == nil || data != nil {
		t.Fatalf("a malformed workflow rendered a ruleset: %s", data)
	}
	bad := config.DefaultPolicy().BranchProtection
	bad.RequiredApprovingReviewers = -1
	_, _, err := RenderRulesetForRepository(t.Context(), t.TempDir(), bad, nil)
	if err == nil || !strings.Contains(err.Error(), RepositoryRulesetPath) {
		t.Fatalf("an invalid policy must fail naming the ruleset, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := RenderRulesetForRepository(ctx, t.TempDir(), config.DefaultPolicy().BranchProtection, nil); err == nil {
		t.Fatal("a cancelled context rendered a ruleset")
	}
}

// A repository without workflows renders a ruleset with no status-check rule, and the policy
// alone decides signatures.
func TestRenderRulesetForRepository_Boundary_NoWorkflows(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	policy.RequireSignedCommits = true
	data, contexts, err := RenderRulesetForRepository(t.Context(), t.TempDir(), policy, nil)
	if err != nil || len(contexts) != 0 {
		t.Fatalf("render: %v, contexts %v", err, contexts)
	}
	if strings.Contains(string(data), "required_status_checks") || !strings.Contains(string(data), "required_signatures") {
		t.Fatalf("unexpected ruleset without workflows:\n%s", data)
	}
}

// renderingInputCases are policies and status contexts RenderRepositoryRuleset renders from,
// covering each rule it may add or leave out and both review modes.
func renderingInputCases() map[string]struct {
	policy   config.BranchProtectionPolicy
	contexts []string
} {
	signed := config.DefaultPolicy().BranchProtection
	signed.RequireSignedCommits, signed.RequiredApprovingReviewers, signed.DismissStaleReviews = true, 3, false
	single := config.DefaultPolicy().BranchProtection
	single.EnforceLinearHistory, single.ReviewMode = false, config.BranchReviewModeSingleMaintainer
	return map[string]struct {
		policy   config.BranchProtectionPolicy
		contexts []string
	}{
		"default, no checks":         {config.DefaultPolicy().BranchProtection, nil},
		"signed, three reviews":      {signed, []string{"test", "Documentation Governance"}},
		"single maintainer, a check": {single, []string{"verify"}},
	}
}

// Every rendering is recognised as one, whatever policy and contexts it was rendered from, in
// either consistent line-ending style.
func TestIsRepositoryRulesetRendering_Positive_RecognisesEveryRendering(t *testing.T) {
	for name, tc := range renderingInputCases() {
		t.Run(name, func(t *testing.T) {
			data, err := RenderRepositoryRuleset(tc.policy, tc.contexts)
			if err != nil {
				t.Fatal(err)
			}
			if !IsRepositoryRulesetRendering(data) {
				t.Fatalf("a rendering is not recognised:\n%s", data)
			}
			if crlf := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")); !IsRepositoryRulesetRendering(crlf) {
				t.Fatal("the CRLF checkout of a rendering is not recognised")
			}
		})
	}
}

// A ruleset the renderer never produces is not one: an operator's own, and a rendering edited in
// its name, rules, layout or line endings.
func TestIsRepositoryRulesetRendering_Negative_EditsAreNotRenderings(t *testing.T) {
	data, err := RenderRepositoryRuleset(config.DefaultPolicy().BranchProtection, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	tabbed, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for name, edited := range map[string]string{
		"operator ruleset":         "{\n  \"name\": \"team-protection\",\n  \"rules\": []\n}\n",
		"final newline added":      text + "\n",
		"renamed":                  strings.Replace(text, RepositoryRulesetName, "team-protection", 1),
		"re-indented":              string(tabbed),
		"rule changed":             strings.Replace(text, `"type": "deletion"`, `"type": "creation"`, 1),
		"code owner review off":    strings.Replace(text, `"require_code_owner_review": true`, `"require_code_owner_review": false`, 1),
		"thread resolution off":    strings.Replace(text, `"required_review_thread_resolution": true`, `"required_review_thread_resolution": false`, 1),
		"mixed line endings":       strings.Replace(text, "\n", "\r\n", 1),
		"not JSON":                 text[:len(text)/2],
		"non-strict status checks": strings.Replace(text, `"strict_required_status_checks_policy": true`, `"strict_required_status_checks_policy": false`, 1),
	} {
		if edited == text {
			t.Fatalf("%s: the edit changed nothing", name)
		}
		if IsRepositoryRulesetRendering([]byte(edited)) {
			t.Errorf("%s is taken for a rendering:\n%s", name, edited)
		}
	}
}

// Empty input, an empty document and a rule list past the bound are no rendering; a duplicated
// status check, which the renderer refuses, is none either.
func TestIsRepositoryRulesetRendering_Boundary_DegenerateInputs(t *testing.T) {
	rules := strings.Repeat(`{"type": "deletion"},`, maxRulesetRules+1)
	for name, data := range map[string]string{
		"empty":           "",
		"empty object":    "{}",
		"null":            "null",
		"too many rules":  `{"rules": [` + strings.TrimSuffix(rules, ",") + `]}`,
		"duplicate check": `{"rules": [{"type": "required_status_checks", "parameters": {"required_status_checks": [{"context": "a"}, {"context": "a"}]}}]}`,
	} {
		if IsRepositoryRulesetRendering([]byte(data)) {
			t.Errorf("%s is taken for a rendering", name)
		}
	}
}

// A planned workflow is read beside the ones on disk: its checks are rendered into the ruleset,
// as they will be once a caller has written it.
func TestRenderRulesetForRepository_Positive_PlannedWorkflowsAreRequired(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	policy := config.DefaultPolicy().BranchProtection
	planned := map[string][]byte{".github/workflows/docs.yml": []byte("on: pull_request\njobs:\n  docs: {}\n")}
	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, planned)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// In workflow file name order, as the run that writes docs.yml reads them.
	if !slices.Equal(contexts, []string{"verify", "docs"}) {
		t.Fatalf("contexts = %v, want [verify docs]", contexts)
	}
	if err := ValidateRepositoryRuleset(data, policy, contexts); err != nil {
		t.Fatalf("the rendering does not validate: %v", err)
	}
}
