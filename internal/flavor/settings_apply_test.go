// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// operatorRuleset is valid JSON an adopter wrote by hand: a ruleset, but not the rendered one.
const operatorRuleset = "{\n  \"name\": \"team-protection\",\n  \"rules\": []\n}\n"

func rulesetPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(forge.RepositoryRulesetPath))
}

// settingOutcome returns what the apply recorded for path, failing the test when it recorded
// nothing.
func settingOutcome(t *testing.T, report *flavor.ApplyReport, path string) flavor.SettingOutcome {
	t.Helper()
	for _, s := range report.Settings {
		if s.Path == path {
			return s
		}
	}
	t.Fatalf("no outcome recorded for %s: %+v", path, report.Settings)
	return flavor.SettingOutcome{}
}

// assertRenderedRuleset checks the ruleset on disk is the one policy renders for the workflows
// the repository now carries.
func assertRenderedRuleset(t *testing.T, dir string, policy config.BranchProtectionPolicy) {
	t.Helper()
	data, err := os.ReadFile(rulesetPath(dir))
	if err != nil {
		t.Fatalf("ruleset not written: %v", err)
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := forge.ValidateRepositoryRuleset(data, policy, contexts); err != nil {
		t.Fatalf("ruleset is not the rendering of the effective policy: %v\n%s", err, data)
	}
}

// A repository that ran only flavor apply carries the branch ruleset, rendered under the built-in
// policy (no .standards.yaml declares another), and passes the flavor audit that requires it.
func TestApplyFlavor_Positive_FreshRepositoryGetsTheRulesetAndPassesTheAudit(t *testing.T) {
	for _, name := range []string{"native-gpu-systems", "infra-k8s"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			report, err := flavor.ApplyFlavor(t.Context(), dir, name, false)
			if err != nil {
				t.Fatalf("apply: %+v, %v", report, err)
			}
			if got := settingOutcome(t, report, forge.RepositoryRulesetPath); got.Action != flavor.SettingCreated {
				t.Fatalf("ruleset outcome = %+v, want created", got)
			}
			assertRenderedRuleset(t, dir, config.DefaultPolicy().BranchProtection)
			audit, err := flavor.AuditFlavor(dir, name)
			if err != nil || !audit.Passed {
				t.Fatalf("the flavor audit must pass after apply alone: %+v, %v", audit, err)
			}
		})
	}
}

// The ruleset is rendered after the templates, so the status checks of the workflows apply just
// wrote are required, and a setting another command writes is named with its producer.
func TestApplyFlavor_Positive_RulesetRequiresTheScaffoldedWorkflows(t *testing.T) {
	dir := t.TempDir()
	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
	if err != nil {
		t.Fatalf("apply: %+v, %v", report, err)
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), dir)
	if err != nil || len(contexts) == 0 {
		t.Fatalf("the scaffolded workflows name no checks: %v, %v", contexts, err)
	}
	assertRenderedRuleset(t, dir, config.DefaultPolicy().BranchProtection)
	for path, producer := range map[string]string{"lefthook.yml": "praetorctl adopt", ".vscode/settings.json": "praetorctl editors generate"} {
		if got := settingOutcome(t, report, path); got.Action != flavor.SettingDeferred || got.Note != producer {
			t.Errorf("%s outcome = %+v, want deferred to %s", path, got, producer)
		}
	}
	again, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
	if err != nil || settingOutcome(t, again, forge.RepositoryRulesetPath).Action != flavor.SettingUnchanged {
		t.Fatalf("a second apply must leave the ruleset unchanged: %+v, %v", again, err)
	}
}

// The effective policy decides what the ruleset requires: overrides in .standards.yaml raise the
// review count and add the signature rule.
func TestApplyFlavor_Positive_EffectivePolicyDecidesTheRuleset(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, config.ManifestFileName), "version: 1\noverrides:\n  branch_protection:\n"+
		"    require_signed_commits: true\n    required_approving_reviewers: 2\n")
	report, err := flavor.ApplyFlavor(t.Context(), dir, "native-gpu-systems", false)
	if err != nil {
		t.Fatalf("apply: %+v, %v", report, err)
	}
	policy := config.DefaultPolicy()
	policy.BranchProtection.RequireSignedCommits = true
	policy.BranchProtection.RequiredApprovingReviewers = 2
	assertRenderedRuleset(t, dir, policy.BranchProtection)
	data, err := os.ReadFile(rulesetPath(dir))
	if err != nil || !strings.Contains(string(data), "required_signatures") || !strings.Contains(string(data), `"required_approving_review_count": 2`) {
		t.Fatalf("the overrides did not reach the ruleset: %v\n%s", err, data)
	}
}

// An adopter-edited ruleset is kept and reported without --force, and replaced with it.
func TestApplyFlavor_Negative_EditedRulesetIsKeptWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(rulesetPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, rulesetPath(dir), operatorRuleset)

	report, err := flavor.ApplyFlavor(t.Context(), dir, "native-gpu-systems", false)
	if err != nil {
		t.Fatalf("apply: %+v, %v", report, err)
	}
	kept := settingOutcome(t, report, forge.RepositoryRulesetPath)
	if kept.Action != flavor.SettingKept || !strings.Contains(kept.Note, "--force replaces it") {
		t.Fatalf("an edited ruleset must be kept and reported, got %+v", kept)
	}
	if data, err := os.ReadFile(rulesetPath(dir)); err != nil || string(data) != operatorRuleset {
		t.Fatalf("the edited ruleset was changed without --force: %q, %v", data, err)
	}

	forced, err := flavor.ApplyFlavor(t.Context(), dir, "native-gpu-systems", true)
	if err != nil || settingOutcome(t, forced, forge.RepositoryRulesetPath).Action != flavor.SettingReplaced {
		t.Fatalf("--force must replace the edited ruleset: %+v, %v", forced, err)
	}
	assertRenderedRuleset(t, dir, config.DefaultPolicy().BranchProtection)
}

// A ruleset adoption.decline refuses is never written, and a decline that cannot be resolved
// fails the ruleset alone.
func TestApplyFlavor_Negative_DeclinedRulesetIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	var asked []string
	declines := func(_ context.Context, step string) (bool, error) {
		asked = append(asked, step)
		return true, nil
	}
	report, err := flavor.ApplyFlavorWith(t.Context(), dir, "native-gpu-systems", flavor.ApplyOptions{Declines: declines})
	if err != nil || settingOutcome(t, report, forge.RepositoryRulesetPath).Action != flavor.SettingDeclined {
		t.Fatalf("a declined ruleset must be reported declined: %+v, %v", report, err)
	}
	if len(asked) != 1 || asked[0] != "branch-ruleset" {
		t.Fatalf("the decline was asked for %v, want [branch-ruleset]", asked)
	}
	if _, err := os.Stat(rulesetPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a declined ruleset was written: %v", err)
	}

	failing := func(context.Context, string) (bool, error) { return false, errors.New("manifest unreadable") }
	report, err = flavor.ApplyFlavorWith(t.Context(), t.TempDir(), "native-gpu-systems", flavor.ApplyOptions{Declines: failing})
	if !errors.Is(err, flavor.ErrApplyIncomplete) || len(report.Errors) != 1 || !strings.Contains(report.Errors[0], "manifest unreadable") {
		t.Fatalf("an unresolved decline must fail the ruleset: %+v, %v", report, err)
	}
}

// TemplatesOnly, adoption's apply, renders and reports no setting at all.
func TestApplyFlavor_Boundary_TemplatesOnlyLeavesSettingsAlone(t *testing.T) {
	dir := t.TempDir()
	report, err := flavor.ApplyFlavorWith(t.Context(), dir, "native-gpu-systems", flavor.ApplyOptions{TemplatesOnly: true})
	if err != nil || len(report.Settings) != 0 || len(report.CreatedTemplates) != 3 {
		t.Fatalf("templates-only apply: %+v, %v", report, err)
	}
	if _, err := os.Stat(rulesetPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("a templates-only apply wrote the ruleset: %v", err)
	}
}

// writeEarlierRendering writes the ruleset Praetor rendered under policy and contexts, the file
// an earlier adoption or apply left before the policy or the workflows changed.
func writeEarlierRendering(t *testing.T, dir string, policy config.BranchProtectionPolicy, contexts []string, crlf bool) string {
	t.Helper()
	data, err := forge.RenderRepositoryRuleset(policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := os.MkdirAll(filepath.Dir(rulesetPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, rulesetPath(dir), text)
	return text
}

// A ruleset Praetor rendered under an earlier policy or before a workflow existed, and nobody
// edited, is refreshed to the current rendering without --force, in its own line-ending style:
// the ruleset adoption wrote before `flavor apply --flavor=<name>` scaffolds that flavor's CI.
func TestApplyFlavor_Positive_RefreshesAnEarlierPraetorRuleset(t *testing.T) {
	signed := config.DefaultPolicy().BranchProtection
	signed.RequireSignedCommits = true
	for name, crlf := range map[string]bool{"LF": false, "CRLF": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeEarlierRendering(t, dir, signed, []string{"Documentation Governance"}, crlf)
			report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
			if err != nil {
				t.Fatalf("apply: %+v, %v", report, err)
			}
			if got := settingOutcome(t, report, forge.RepositoryRulesetPath); got.Action != flavor.SettingRefreshed {
				t.Fatalf("an earlier Praetor ruleset must be refreshed, got %+v", got)
			}
			assertRenderedRuleset(t, dir, config.DefaultPolicy().BranchProtection)
			data, err := os.ReadFile(rulesetPath(dir))
			if err != nil {
				t.Fatal(err)
			}
			allCRLF := strings.Count(string(data), "\r\n") == strings.Count(string(data), "\n")
			if allCRLF != crlf {
				t.Fatalf("the refresh did not keep the file's line endings (crlf=%v):\n%q", crlf, data)
			}
		})
	}
}

// A Praetor rendering someone edited, here only by saving it with a final newline, is no longer
// one: it keeps the --force contract of any adopter-edited ruleset.
func TestApplyFlavor_Negative_EditedRenderingIsNotRefreshed(t *testing.T) {
	dir := t.TempDir()
	edited := writeEarlierRendering(t, dir, config.DefaultPolicy().BranchProtection, nil, false) + "\n"
	mustWriteFile(t, rulesetPath(dir), edited)
	report, err := flavor.ApplyFlavor(t.Context(), dir, "go-service", false)
	if err != nil {
		t.Fatalf("apply: %+v, %v", report, err)
	}
	if got := settingOutcome(t, report, forge.RepositoryRulesetPath); got.Action != flavor.SettingKept {
		t.Fatalf("an edited rendering must be kept, got %+v", got)
	}
	if data, err := os.ReadFile(rulesetPath(dir)); err != nil || string(data) != edited {
		t.Fatalf("the edited rendering was changed without --force: %q, %v", data, err)
	}
}
