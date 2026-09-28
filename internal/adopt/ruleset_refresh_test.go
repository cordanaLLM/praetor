// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
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
	if err := forge.ValidateRepositoryRuleset(before, "main", config.DefaultPolicy().BranchProtection, contexts); err != nil {
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
	data, err := forge.RenderRepositoryRuleset("main", signed, append(slices.Clone(contexts), "e2e"))
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
	data, err := forge.RenderRepositoryRuleset("main", config.DefaultPolicy().BranchProtection, nil)
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
	data, err := forge.RenderRepositoryRuleset("main", config.DefaultPolicy().BranchProtection, nil)
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

// recordOriginHead points the test checkout's origin HEAD at branch, as git clone records it.
func recordOriginHead(t *testing.T, repo, branch string) {
	t.Helper()
	mustWrite(t, filepath.Join(repo, ".git", "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/"+branch+"\n")
}

// readRuleset returns the ruleset adoption left in repo.
func readRuleset(t *testing.T, repo string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, rulesetFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// manifestDetails returns what adoption reported about .standards.yaml.
func manifestDetails(rep *AdoptReport) string {
	var details []string
	for _, action := range rep.ActionDetails {
		if action.Path == manifestFile {
			details = append(details, action.Details)
		}
	}
	return strings.Join(details, "\n")
}

// undeclaredBranchWarnings returns the adoption warnings about a manifest that leaves the default
// branch undeclared.
func undeclaredBranchWarnings(rep *AdoptReport) []string {
	var warnings []string
	for _, w := range rep.Warnings {
		if strings.Contains(w, "declares no repository.default_branch") {
			warnings = append(warnings, w)
		}
	}
	return warnings
}

// removeOriginHead drops the test checkout's origin HEAD, as a CI checkout has none.
func removeOriginHead(t *testing.T, repo string) {
	t.Helper()
	if err := os.Remove(filepath.Join(repo, ".git", "refs", "remotes", "origin", "HEAD")); err != nil {
		t.Fatal(err)
	}
}

// A repository whose default branch is master gets a ruleset protecting master, and the manifest
// adoption creates declares it: a checkout without the origin HEAD, as CI checks out, resolves
// master from .standards.yaml and the audit passes there too.
func TestAdopt_Positive_MasterRepositoryRulesetProtectsMaster(t *testing.T) {
	repo := newTestRepo(t, "master-default")
	recordOriginHead(t, repo, "master")
	rep := adoptForRuleset(t, repo, false)
	if written := readRuleset(t, repo); !strings.Contains(written, `"refs/heads/master"`) || strings.Contains(written, `"refs/heads/main"`) {
		t.Fatalf("a master repository's ruleset must protect master and not main:\n%s", written)
	}
	manifest, err := config.LoadManifest(filepath.Join(repo, config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Repository.DefaultBranch != "master" {
		t.Fatalf("the created manifest must declare the default branch master, declares %q", manifest.Repository.DefaultBranch)
	}
	if details := manifestDetails(rep); !strings.Contains(details, "repository.default_branch: master recorded from the origin remote's HEAD") {
		t.Fatalf("the report must say where the declared branch came from: %q", details)
	}
	if warnings := undeclaredBranchWarnings(rep); len(warnings) != 0 {
		t.Fatalf("a manifest adoption created must not be warned about: %v", warnings)
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("the audit must pass for a master repository: %q, %v", summary, err)
	}
	removeOriginHead(t, repo)
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("a checkout without origin HEAD must pass the audit on the declared branch: %q, %v", summary, err)
	}
}

// An existing manifest that declares no default branch is never rewritten: in a master checkout
// adoption renders master and warns that a checkout without the origin HEAD renders main, which
// the audit there reports as drift. A declared or a declined ruleset draws no warning. An origin
// HEAD the ruleset cannot carry fails adoption before any step writes.
func TestAdopt_Negative_UndeclaredBranchInAnExistingManifestIsWarned(t *testing.T) {
	repo := newTestRepo(t, "existing-undeclared")
	recordOriginHead(t, repo, "master")
	mustWrite(t, filepath.Join(repo, config.ManifestFileName), "version: 1\nrepository:\n  owner: acme\n  name: existing-undeclared\n")
	rep := adoptForRuleset(t, repo, false)
	warnings := undeclaredBranchWarnings(rep)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Declare repository.default_branch: master") {
		t.Fatalf("an existing manifest without the branch must be warned about once, naming master: %v", rep.Warnings)
	}
	manifest, err := config.LoadManifest(filepath.Join(repo, config.ManifestFileName))
	if err != nil || manifest.Repository.DefaultBranch != "" {
		t.Fatalf("adoption must not rewrite the existing manifest's repository block: %+v, %v", manifest, err)
	}
	removeOriginHead(t, repo)
	if _, err := auditAdoptedRuleset(t, repo); err == nil || !strings.Contains(err.Error(), forge.ErrRulesetDrift.Error()) {
		t.Fatalf("the warned-about checkout without origin HEAD must render main and report drift, got %v", err)
	}

	for name, body := range map[string]string{
		"declared": "version: 1\nrepository:\n  owner: acme\n  name: quiet\n  default_branch: master\n",
		"declined": "version: 1\nrepository:\n  owner: acme\n  name: quiet\nadoption:\n  decline: [branch-ruleset]\n",
	} {
		quiet := newTestRepo(t, "quiet-"+name)
		recordOriginHead(t, quiet, "master")
		mustWrite(t, filepath.Join(quiet, config.ManifestFileName), body)
		if warnings := undeclaredBranchWarnings(adoptForRuleset(t, quiet, false)); len(warnings) != 0 {
			t.Fatalf("%s: no default-branch warning expected, got %v", name, warnings)
		}
	}

	unusable := newTestRepo(t, "unusable-origin-head")
	recordOriginHead(t, unusable, "_private")
	rep, err = Adopt(t.Context(), AdoptOptions{Path: unusable, SkipGitValidation: true, LockSourceRoot: newAdoptLockSource(t)})
	if err == nil || !strings.Contains(err.Error(), "repository.default_branch") || rep == nil || len(rep.CreatedFiles) != 0 {
		t.Fatalf("an origin HEAD the ruleset cannot carry must fail before any write: %v, report %+v", err, rep)
	}
	if _, err := os.Stat(filepath.Join(unusable, config.ManifestFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed default-branch read must leave no manifest: %v", err)
	}
}

// Nothing is declared where every checkout resolves the same branch: an origin HEAD at main and a
// checkout recording none create a manifest without repository.default_branch. A dry run in a
// master checkout writes nothing, the manifest included.
func TestAdopt_Boundary_MainOrNoOriginHeadDeclaresNothing(t *testing.T) {
	for name, head := range map[string]string{"main": forge.FallbackDefaultBranch, "none": ""} {
		repo := newTestRepo(t, "declares-nothing-"+name)
		if head != "" {
			recordOriginHead(t, repo, head)
		}
		rep := adoptForRuleset(t, repo, false)
		if manifest := mustRead(t, filepath.Join(repo, config.ManifestFileName)); strings.Contains(manifest, "default_branch") {
			t.Fatalf("origin HEAD %q: the manifest must declare no default branch:\n%s", head, manifest)
		}
		if strings.Contains(manifestDetails(rep), "default_branch") || len(undeclaredBranchWarnings(rep)) != 0 {
			t.Fatalf("origin HEAD %q: nothing to report about the default branch: %q, %v", head, manifestDetails(rep), rep.Warnings)
		}
		if written := readRuleset(t, repo); !strings.Contains(written, `"refs/heads/main"`) {
			t.Fatalf("origin HEAD %q: the ruleset must protect main:\n%s", head, written)
		}
	}
	dry := newTestRepo(t, "dry-run-master")
	recordOriginHead(t, dry, "master")
	adoptForRuleset(t, dry, true)
	if _, err := os.Stat(filepath.Join(dry, config.ManifestFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a dry run must not write the manifest: %v", err)
	}
}

// The ruleset every Praetor before this one wrote for a master repository protects main. It is
// Praetor's unedited rendering, so a plain adoption refreshes it to master without --force and
// the audit passes; the dry run previews that update. The same file in a main repository is
// current and stays unchanged.
func TestAdopt_Positive_RefreshesTheEarlierMainRulesetOfAMasterRepository(t *testing.T) {
	earlier, err := forge.RenderRepositoryRuleset(forge.FallbackDefaultBranch, config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	repo := newTestRepo(t, "master-earlier-main")
	recordOriginHead(t, repo, "master")
	mustWrite(t, filepath.Join(repo, rulesetFile), string(earlier))
	if preview := dryRunRulesetPreview(t, repo, false); preview.Action != PreviewUpdate ||
		!strings.Contains(preview.Diff, "\n+        \"refs/heads/master\",\n") {
		t.Fatalf("the dry run must preview the refresh to master, got %+v", preview)
	}
	rep := adoptForRuleset(t, repo, false)
	if warnings := rulesetWarnings(rep); len(warnings) != 0 {
		t.Fatalf("the earlier main rendering was reported as drift: %v", warnings)
	}
	if written := readRuleset(t, repo); !strings.Contains(written, `"refs/heads/master"`) || strings.Contains(written, `"refs/heads/main"`) {
		t.Fatalf("the earlier main rendering must be refreshed to master:\n%s", written)
	}
	if _, err := auditAdoptedRuleset(t, repo); err != nil {
		t.Fatalf("the refreshed ruleset must pass the audit: %v", err)
	}

	mainRepo := newTestRepo(t, "main-earlier-main")
	recordOriginHead(t, mainRepo, "main")
	mustWrite(t, filepath.Join(mainRepo, rulesetFile), string(earlier))
	if preview := dryRunRulesetPreview(t, mainRepo, false); preview.Action != PreviewUpdate {
		t.Fatalf("a main repository refreshes its earlier rendering to the adopted policy, got %+v", preview)
	}
	adoptForRuleset(t, mainRepo, false)
	if written := readRuleset(t, mainRepo); !strings.Contains(written, `"refs/heads/main"`) {
		t.Fatalf("a main repository's ruleset must keep protecting main:\n%s", written)
	}
}

// A declared custom default branch beats the origin HEAD, and a ruleset edited to protect
// another branch is the repository's: kept with a warning, not refreshed.
func TestAdopt_Negative_DeclaredBranchWinsAndAnEditedBranchIsKept(t *testing.T) {
	repo := newTestRepo(t, "declared-trunk")
	recordOriginHead(t, repo, "master")
	mustWrite(t, filepath.Join(repo, config.ManifestFileName), "version: 1\nrepository:\n  owner: acme\n  name: declared-trunk\n  default_branch: trunk\n")
	edited, err := forge.RenderRepositoryRuleset("develop", config.DefaultPolicy().BranchProtection, nil)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repo, rulesetFile), string(edited))
	rep := adoptForRuleset(t, repo, false)
	if len(rulesetWarnings(rep)) != 1 || readRuleset(t, repo) != string(edited) {
		t.Fatalf("a ruleset for another branch must be kept with one warning: %v", rep.Warnings)
	}
	if _, err := Adopt(t.Context(), AdoptOptions{Path: repo, Force: true, SkipGitValidation: true, LockSourceRoot: newAdoptLockSource(t)}); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	if written := readRuleset(t, repo); !strings.Contains(written, `"refs/heads/trunk"`) || strings.Contains(written, `"refs/heads/master"`) {
		t.Fatalf("the declared branch must win over origin HEAD:\n%s", written)
	}
	if summary, err := auditAdoptedRuleset(t, repo); err != nil || !strings.HasPrefix(summary, "[PASS]") {
		t.Fatalf("the audit must pass for the declared branch: %q, %v", summary, err)
	}
}

// Boundary: audit compares the ruleset only while the policy enforces linear history or signed
// commits (rulesetRequired), so only then may --force replace a differing one. Under a policy
// requiring neither, --force keeps it byte for byte with the not-audit-verified note; the same
// ruleset under a policy requiring linear history is replaced.
func TestReconcileBranchRuleset_Boundary_ForceReplacesOnlyWhilePolicyRequiresIt(t *testing.T) {
	const handManaged = "{\"name\": \"hand-managed\"}\n"
	for _, tc := range []struct {
		name     string
		linear   bool
		replaced bool
	}{{"neither linear nor signed", false, false}, {"linear history", true, true}} {
		repo := newTestRepo(t, "ruleset-lock")
		mustWrite(t, filepath.Join(repo, filepath.FromSlash(rulesetFile)), handManaged)
		policy := config.DefaultPolicy()
		policy.BranchProtection.EnforceLinearHistory = tc.linear
		policy.BranchProtection.RequireSignedCommits = false
		s := &adoptSession{repoPath: repo, report: &AdoptReport{}, opts: AdoptOptions{Force: true},
			policy: &config.EffectivePolicy{Policy: *policy}}
		if err := reconcileBranchRuleset(t.Context(), s); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		kept := mustRead(t, filepath.Join(repo, filepath.FromSlash(rulesetFile))) == handManaged
		if kept == tc.replaced || hasAction(s.report, rulesetFile, actionReplace) != tc.replaced {
			t.Fatalf("%s: kept=%v, want replaced=%v; actions %+v", tc.name, kept, tc.replaced, s.report.ActionDetails)
		}
		if detail := findActionDetail(s.report.ActionDetails, rulesetFile); !tc.replaced && !strings.Contains(detail, "not audit-verified; kept") {
			t.Errorf("%s: drift note %q", tc.name, detail)
		}
	}
}
