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
	if err != nil || !forge.IsRepositoryRulesetRendering(before) {
		t.Fatalf("flavor apply must leave a Praetor rendering: %v\n%s", err, before)
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
