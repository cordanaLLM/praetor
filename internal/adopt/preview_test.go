// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// operatorRulesetPreview is a ruleset an adopter wrote by hand: valid JSON, not the rendering.
const operatorRulesetPreview = "{\n  \"name\": \"team-protection\",\n  \"rules\": []\n}\n"

// dryRunRulesetPreview runs a dry adoption of repo and returns the ruleset preview, asserting
// the run wrote nothing and recorded exactly one preview.
func dryRunRulesetPreview(t *testing.T, repo string, force bool) FilePreview {
	t.Helper()
	before := snapshotTree(t, repo)
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, DryRun: true, Force: force, SkipGitValidation: true})
	if err != nil {
		t.Fatalf("dry-run adopt: %v", err)
	}
	assertTreeUnchanged(t, before, snapshotTree(t, repo))
	if len(report.Previews) != 1 || report.Previews[0].Path != rulesetFile {
		t.Fatalf("expected one ruleset preview, got %+v", report.Previews)
	}
	return report.Previews[0]
}

// A dry run on a repository without a ruleset previews its creation with the rendered content,
// and a ruleset holding that content previews as unchanged.
func TestAdoptDryRun_Positive_PreviewsCreateThenUnchanged(t *testing.T) {
	repo := newTestRepo(t, "preview-create")
	created := dryRunRulesetPreview(t, repo, false)
	if created.Action != PreviewCreate || created.Diff != "" || !strings.Contains(created.Note, "required status checks") {
		t.Fatalf("absent ruleset preview = %+v", created)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(created.Content), &doc); err != nil || doc["name"] != "praetor-main-protection" {
		t.Fatalf("create must carry the rendered ruleset: %v\n%s", err, created.Content)
	}

	mustWrite(t, filepath.Join(repo, rulesetFile), created.Content)
	unchanged := dryRunRulesetPreview(t, repo, false)
	if unchanged.Action != PreviewUnchanged || unchanged.Content != "" || unchanged.Diff != "" {
		t.Fatalf("the rendered ruleset must preview unchanged, got %+v", unchanged)
	}
}

// A differing ruleset previews as an update with the diff under --force, and as kept with the
// same diff without it; neither run writes it.
func TestAdoptDryRun_Negative_DifferingRulesetShowsTheDiff(t *testing.T) {
	repo := newTestRepo(t, "preview-update")
	mustWrite(t, filepath.Join(repo, rulesetFile), operatorRulesetPreview)

	updated := dryRunRulesetPreview(t, repo, true)
	if updated.Action != PreviewUpdate || updated.Content != "" {
		t.Fatalf("--force preview of a differing ruleset = %+v", updated)
	}
	for _, line := range []string{"--- a/" + rulesetFile, "+++ b/" + rulesetFile, "-  \"name\": \"team-protection\",", "+  \"name\": \"praetor-main-protection\","} {
		if !strings.Contains(updated.Diff, line+"\n") {
			t.Fatalf("update diff lacks %q:\n%s", line, updated.Diff)
		}
	}

	kept := dryRunRulesetPreview(t, repo, false)
	if kept.Action != PreviewKeep || kept.Diff != updated.Diff {
		t.Fatalf("without --force the differing ruleset must preview kept with the same diff, got %+v", kept)
	}
	if mustRead(t, filepath.Join(repo, rulesetFile)) != operatorRulesetPreview {
		t.Fatal("a dry run rewrote the ruleset")
	}
}

// Only a dry run previews: a real run writes the ruleset and records no preview.
func TestAdoptDryRun_Boundary_RealRunRecordsNoPreview(t *testing.T) {
	repo := newTestRepo(t, "preview-real")
	report, err := Adopt(t.Context(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t), SkipGitValidation: true})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if len(report.Previews) != 0 {
		t.Fatalf("a real run recorded previews: %+v", report.Previews)
	}
	if !contains(report.CreatedFiles, rulesetFile) {
		t.Fatalf("the real run did not write the ruleset: %v", report.CreatedFiles)
	}
}
