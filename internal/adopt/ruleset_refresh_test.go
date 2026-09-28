// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// auditAdoptedRuleset runs the branch protection audit against the effective policy of the
// adopted repository at repo, the check `praetorctl audit` applies after adoption.
func auditAdoptedRuleset(t *testing.T, repo string) (string, error) {
	t.Helper()
	manifestPath := filepath.Join(repo, config.ManifestFileName)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load the adopted manifest: %v", err)
	}
	policy, _, err := config.ResolveRepositoryPolicy(t.Context(), manifestPath, manifest)
	if err != nil {
		t.Fatalf("resolve the adopted policy: %v", err)
	}
	return AuditBranchProtectionWithPolicy(t.Context(), manifest, repo, policy)
}

// rulesetWarnings returns the adoption warnings about the ruleset.
func rulesetWarnings(rep *AdoptReport) []string {
	var warnings []string
	for _, w := range rep.Warnings {
		if strings.HasPrefix(w, rulesetFile+":") {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// adoptForRuleset adopts repo for real with a verified lock source.
func adoptForRuleset(t *testing.T, repo string, dryRun bool) *AdoptReport {
	t.Helper()
	rep, err := Adopt(t.Context(), AdoptOptions{Path: repo, DryRun: dryRun, SkipGitValidation: true, LockSourceRoot: newAdoptLockSource(t)})
	if err != nil {
		t.Fatalf("adopt (dry run %v): %v", dryRun, err)
	}
	return rep
}

// flavor apply before adoption renders the ruleset under the built-in policy and the flavor's
// workflows. Adoption then pins an archetype policy and adds the documentation gate: it refreshes
// that unedited Praetor ruleset instead of keeping it as drift, and the audit passes, as it does
// when adoption runs alone.
func TestAdopt_Positive_RefreshesTheRulesetFlavorApplyWrote(t *testing.T) {
	repo := newTestRepo(t, "apply-then-adopt")
	mustWrite(t, filepath.Join(repo, "meson.build"), "project('x', 'c')\n")
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "native-gpu-systems", false); err != nil {
		t.Fatalf("flavor apply: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := forge.ValidateRepositoryRuleset(before, config.DefaultPolicy().BranchProtection, contexts); err != nil {
		t.Fatalf("flavor apply must leave the built-in policy's rendering: %v\n%s", err, before)
	}

	preview := dryRunRulesetPreview(t, repo, false)
	if preview.Action != PreviewUpdate || preview.Diff == "" {
		t.Fatalf("the dry run must preview the refresh with its diff, got %+v", preview)
	}
	rep := adoptForRuleset(t, repo, false)
	if warnings := rulesetWarnings(rep); len(warnings) != 0 {
		t.Fatalf("an unedited Praetor ruleset was reported as drift: %v", warnings)
	}
	if !slices.ContainsFunc(rep.ActionDetails, func(d ActionDetail) bool {
		return d.Path == rulesetFile && strings.HasPrefix(d.Details, "Refreshed the unedited earlier Praetor branch protection ruleset")
	}) {
		t.Fatalf("the refresh is not reported: %+v", rep.ActionDetails)
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the audit must pass after apply then adopt: %v", err)
	} else if !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("audit summary = %q", summary)
	}
}

// After flavor apply, an adopter who only edits values of the ruleset, here a signature rule and a
// hand-added status check, keeps them: adoption keeps the file with one warning instead of taking
// it for the rendering flavor apply wrote, and --force replaces it.
func TestAdopt_Negative_ValueEditAfterFlavorApplyIsKept(t *testing.T) {
	repo := newTestRepo(t, "apply-edit-adopt")
	mustWrite(t, filepath.Join(repo, "meson.build"), "project('x', 'c')\n")
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "native-gpu-systems", false); err != nil {
		t.Fatalf("flavor apply: %v", err)
	}
	contexts, err := forge.RequiredStatusContexts(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	signed := config.DefaultPolicy().BranchProtection
	signed.RequireSignedCommits = true
	data, err := forge.RenderRepositoryRuleset(signed, append(slices.Clone(contexts), "e2e"))
	if err != nil {
		t.Fatal(err)
	}
	edited := string(data)
	mustWrite(t, filepath.Join(repo, rulesetFile), edited)

	if preview := dryRunRulesetPreview(t, repo, false); preview.Action != PreviewKeep {
		t.Fatalf("a value-edited ruleset must preview as keep, got %+v", preview)
	}
	rep := adoptForRuleset(t, repo, false)
	if len(rulesetWarnings(rep)) != 1 {
		t.Fatalf("a value-edited ruleset must be kept with one warning: %v", rep.Warnings)
	}
	if got, err := os.ReadFile(filepath.Join(repo, rulesetFile)); err != nil || string(got) != edited {
		t.Fatalf("the value-edited ruleset was changed without --force: %q, %v", got, err)
	}
	if _, err := Adopt(t.Context(), AdoptOptions{Path: repo, Force: true, SkipGitValidation: true, LockSourceRoot: newAdoptLockSource(t)}); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repo, rulesetFile)); err != nil || string(got) == edited {
		t.Fatalf("--force must replace the value-edited ruleset: %v", err)
	}
	if _, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the replaced ruleset must pass the audit: %v", err)
	}
}

// Adoption, then flavor apply of a flavor that adds a workflow: apply refreshes the ruleset
// adoption wrote, which was current before it ran, and the audit passes.
func TestAdopt_Positive_FlavorApplyAfterAdoptionRefreshesTheRuleset(t *testing.T) {
	repo := newTestRepo(t, "adopt-then-apply")
	mustWrite(t, filepath.Join(repo, "meson.build"), "project('x', 'c')\n")
	adoptForRuleset(t, repo, false)
	before, err := forge.RequiredStatusContexts(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := flavor.ApplyFlavor(t.Context(), repo, "go-service", false)
	if err != nil {
		t.Fatalf("flavor apply: %+v, %v", applied, err)
	}
	after, err := forge.RequiredStatusContexts(t.Context(), repo)
	if err != nil || len(after) <= len(before) {
		t.Fatalf("the fixture must add a workflow check: before %v, after %v, %v", before, after, err)
	}
	if !slices.ContainsFunc(applied.Settings, func(o flavor.SettingOutcome) bool {
		return o.Path == rulesetFile && o.Action == flavor.SettingRefreshed
	}) {
		t.Fatalf("flavor apply must refresh the ruleset adoption wrote: %+v", applied.Settings)
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("the audit must pass after adopt then apply: %q, %v", summary, err)
	}
}

// A Praetor rendering someone edited, here only by a final newline an editor added, is the
// repository's: adoption keeps it and warns, as it keeps any other differing ruleset.
func TestAdopt_Negative_EditedRenderingIsKept(t *testing.T) {
	repo := newTestRepo(t, "edited-rendering")
	data, err := forge.RenderRepositoryRuleset(config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(data) + "\n"
	mustWrite(t, filepath.Join(repo, rulesetFile), edited)
	if preview := dryRunRulesetPreview(t, repo, false); preview.Action != PreviewKeep {
		t.Fatalf("an edited rendering must preview as keep, got %+v", preview)
	}
	rep := adoptForRuleset(t, repo, false)
	if len(rulesetWarnings(rep)) != 1 {
		t.Fatalf("an edited rendering must be kept with one warning: %v", rep.Warnings)
	}
	if got, err := os.ReadFile(filepath.Join(repo, rulesetFile)); err != nil || string(got) != edited {
		t.Fatalf("the edited rendering was changed without --force: %q, %v", got, err)
	}
}

// On a first adoption the dry run previews the ruleset the run writes, status checks of the
// workflows the run scaffolds (the flavor's CI, the documentation gate) included, byte for byte.
func TestAdoptDryRun_Positive_FirstAdoptionPreviewIsTheWrittenRuleset(t *testing.T) {
	repo := newTestRepo(t, "preview-first-adoption")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/app\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(repo, "cmd/app/main.go"), "package main\n\nfunc main() {}\n")
	onDisk, err := forge.RequiredStatusContexts(t.Context(), repo)
	if err != nil || len(onDisk) != 0 {
		t.Fatalf("the fixture must start without workflow checks: %v, %v", onDisk, err)
	}

	preview := dryRunRulesetPreview(t, repo, false)
	if preview.Action != PreviewCreate || !strings.Contains(preview.Content, "Documentation Governance") {
		t.Fatalf("the preview must require the checks the run scaffolds, got %+v", preview)
	}
	adoptForRuleset(t, repo, false)
	written, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Content != string(written) {
		t.Fatalf("the dry run previewed a ruleset the run does not write:\npreview:\n%s\nwritten:\n%s", preview.Content, written)
	}
}

// A CRLF checkout of an earlier rendering is refreshed in CRLF, and the dry run previews those
// bytes: every added line of its diff keeps the file's line ending.
func TestAdopt_Boundary_CRLFRenderingRefreshesInItsOwnLineEndings(t *testing.T) {
	repo := newTestRepo(t, "crlf-rendering")
	data, err := forge.RenderRepositoryRuleset(config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, rulesetFile), strings.ReplaceAll(string(data), "\n", "\r\n"))

	preview := dryRunRulesetPreview(t, repo, false)
	if preview.Action != PreviewUpdate {
		t.Fatalf("a CRLF earlier rendering must preview as update, got %+v", preview)
	}
	for _, line := range strings.SplitAfter(preview.Diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") && !strings.HasSuffix(line, "\r\n") {
			t.Fatalf("the preview adds a line the run does not write, %q, in:\n%s", line, preview.Diff)
		}
	}
	adoptForRuleset(t, repo, false)
	written, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil || strings.Count(string(written), "\r\n") != strings.Count(string(written), "\n") {
		t.Fatalf("the refresh must keep CRLF: %v\n%q", err, written)
	}
	if _, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the refreshed CRLF ruleset must pass the audit: %v", err)
	}
}
