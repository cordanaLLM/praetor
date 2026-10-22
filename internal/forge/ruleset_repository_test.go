// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
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
	want, err := RenderRepositoryRuleset("main", policy, contexts)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("rendering differs from RenderRepositoryRuleset: %v\n%s", err, data)
	}
	if err := ValidateRepositoryRuleset(data, "main", policy, contexts); err != nil {
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

// The digest PriorRulesetDigests returns finds the baseline's rendering, whatever policy and
// contexts it was rendered from, in either consistent line-ending style.
func TestPriorRulesetDigests_Positive_FindsTheBaselineRendering(t *testing.T) {
	for name, tc := range renderingInputCases() {
		t.Run(name, func(t *testing.T) {
			data, err := RenderRepositoryRuleset("main", tc.policy, tc.contexts)
			if err != nil {
				t.Fatal(err)
			}
			current, err := RenderRepositoryRuleset("main", tc.policy, append(slices.Clone(tc.contexts), "added"))
			if err != nil {
				t.Fatal(err)
			}
			prior := PriorRulesetDigests(RulesetBaseline{Policy: tc.policy, Contexts: tc.contexts}, current)
			if _, known, crlf := util.LookupCanonicalText(data, prior); !known || crlf {
				t.Fatalf("the baseline rendering is not found (known %v, crlf %v):\n%s", known, crlf, data)
			}
			crlfData := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
			if _, known, crlf := util.LookupCanonicalText(crlfData, prior); !known || !crlf {
				t.Fatalf("the CRLF checkout of the baseline rendering is not found (known %v, crlf %v)", known, crlf)
			}
		})
	}
}

// Nothing is read back from a file: a rendering with one value edited is a rendering under other
// inputs, and matches no digest, as an operator's own ruleset and a re-laid-out rendering do not.
func TestPriorRulesetDigests_Negative_ValueEditsMatchNothing(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	contexts := []string{"test"}
	current, err := RenderRepositoryRuleset("main", policy, append(slices.Clone(contexts), "Documentation Governance"))
	if err != nil {
		t.Fatal(err)
	}
	prior := PriorRulesetDigests(RulesetBaseline{Policy: policy, Contexts: contexts}, current)
	if len(prior) != 1 {
		t.Fatalf("the baseline must yield one digest, got %v", prior)
	}
	signed, reviews, stale, linear := policy, policy, policy, policy
	signed.RequireSignedCommits = true
	reviews.ReviewMode, reviews.RequiredApprovingReviewers = config.BranchReviewModeIndependent, 3
	stale.DismissStaleReviews = !policy.DismissStaleReviews
	linear.EnforceLinearHistory = !policy.EnforceLinearHistory
	render := func(p config.BranchProtectionPolicy, c []string) string {
		data, err := RenderRepositoryRuleset("main", p, c)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	text := render(policy, contexts)
	for name, edited := range map[string]string{
		"signature rule added":     render(signed, contexts),
		"review count raised":      render(reviews, contexts),
		"dismiss stale flipped":    render(stale, contexts),
		"linear history flipped":   render(linear, contexts),
		"status check added":       render(policy, []string{"test", "e2e"}),
		"status check removed":     render(policy, nil),
		"signature and e2e added":  render(signed, []string{"test", "e2e"}),
		"final newline added":      text + "\n",
		"renamed":                  strings.Replace(text, RepositoryRulesetName, "team-protection", 1),
		"mixed line endings":       strings.Replace(text, "\n", "\r\n", 1),
		"operator ruleset":         "{\n  \"name\": \"team-protection\",\n  \"rules\": []\n}\n",
		"non-strict status checks": strings.Replace(text, `"strict_required_status_checks_policy": true`, `"strict_required_status_checks_policy": false`, 1),
	} {
		if edited == text {
			t.Fatalf("%s: the edit changed nothing", name)
		}
		if _, known, _ := util.LookupCanonicalText([]byte(edited), prior); known {
			t.Errorf("%s is taken for the baseline rendering:\n%s", name, edited)
		}
	}
}

// A baseline whose rendering is current needs no refresh, in either line-ending style, and one
// the renderer refuses (a negative review count, a duplicated check) yields no digest.
func TestPriorRulesetDigests_Boundary_CurrentOrUnrenderableBaseline(t *testing.T) {
	policy := config.DefaultPolicy().BranchProtection
	current, err := RenderRepositoryRuleset("main", policy, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	baseline := RulesetBaseline{Policy: policy, Contexts: []string{"test"}}
	crlfCurrent := bytes.ReplaceAll(current, []byte("\n"), []byte("\r\n"))
	for name, content := range map[string][]byte{"current": current, "current in CRLF": crlfCurrent} {
		if prior := PriorRulesetDigests(baseline, content); len(prior) != 0 {
			t.Errorf("%s: the current rendering must yield no earlier text, got %v", name, prior)
		}
	}
	bad := policy
	bad.RequiredApprovingReviewers = -1
	for name, b := range map[string]RulesetBaseline{
		"negative review count": {Policy: bad},
		"duplicate check":       {Policy: policy, Contexts: []string{"a", "a"}},
	} {
		if prior := PriorRulesetDigests(b, current); len(prior) != 0 {
			t.Errorf("%s: an unrenderable baseline yielded %v", name, prior)
		}
	}
}

// A repository's baseline is its effective policy, the built-in default without a manifest and
// the manifest's overrides with one, and the checks of the workflows it carries.
func TestReadRulesetBaseline_Positive_PolicyAndWorkflows(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	baseline, err := ReadRulesetBaseline(t.Context(), root)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if !reflect.DeepEqual(baseline.Policy, config.DefaultPolicy().BranchProtection) || !slices.Equal(baseline.Contexts, []string{"verify"}) {
		t.Fatalf("baseline without a manifest = %+v", baseline)
	}
	writeManifestFixture(t, root, "version: 1\noverrides:\n  branch_protection:\n    require_signed_commits: true\n")
	baseline, err = ReadRulesetBaseline(t.Context(), root)
	if err != nil || !baseline.Policy.RequireSignedCommits {
		t.Fatalf("the manifest's overrides must reach the baseline: %+v, %v", baseline, err)
	}
}

// A manifest that does not resolve and a workflow that does not parse leave no baseline, each
// named against the ruleset.
func TestReadRulesetBaseline_Negative_UnreadableInputs(t *testing.T) {
	unresolved := t.TempDir()
	writeManifestFixture(t, unresolved, "version: [\n")
	if _, err := ReadRulesetBaseline(t.Context(), unresolved); err == nil || !strings.Contains(err.Error(), "resolve the effective policy for "+RepositoryRulesetPath) {
		t.Fatalf("an unresolvable manifest must fail naming the ruleset, got %v", err)
	}
	malformed := t.TempDir()
	writeWorkflowFixture(t, malformed, "ci.yml", "jobs: [")
	if _, err := ReadRulesetBaseline(t.Context(), malformed); err == nil || !strings.Contains(err.Error(), RepositoryRulesetPath) {
		t.Fatalf("a malformed workflow must fail naming the ruleset, got %v", err)
	}
}

// An empty repository's baseline is the built-in policy with no checks; a cancelled context
// reads none.
func TestReadRulesetBaseline_Boundary_EmptyRepositoryAndCancelledContext(t *testing.T) {
	baseline, err := ReadRulesetBaseline(t.Context(), t.TempDir())
	if err != nil || len(baseline.Contexts) != 0 || !reflect.DeepEqual(baseline.Policy, config.DefaultPolicy().BranchProtection) {
		t.Fatalf("empty repository baseline = %+v, %v", baseline, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", "on: pull_request\njobs:\n  verify: {}\n")
	if _, err := ReadRulesetBaseline(ctx, root); err == nil {
		t.Fatal("a cancelled context read a baseline")
	}
}

// writeManifestFixture writes the repository manifest at root.
func writeManifestFixture(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.ManifestFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
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
	if err := ValidateRepositoryRuleset(data, "main", policy, contexts); err != nil {
		t.Fatalf("the rendering does not validate: %v", err)
	}
}
