// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	// agitRow is the AGit push only a Forgejo harness carries.
	agitRow = "HEAD:refs/for/main"
	// reviewBranchRow is the push every forge's harness carries.
	reviewBranchRow = "git push origin HEAD:refs/heads/paperclip/<issue-id>"
)

// declareForge adds repository.forge to the adopted manifest, as an operator editing it does.
func declareForge(t *testing.T, repo, forge string) {
	t.Helper()
	path := filepath.Join(repo, manifestFile)
	manifest := mustRead(t, path)
	if !strings.Contains(manifest, "\nrepository:\n") {
		t.Fatalf("fixture manifest has no repository block:\n%s", manifest)
	}
	mustWrite(t, path, strings.Replace(manifest, "\nrepository:\n", "\nrepository:\n  forge: "+forge+"\n", 1))
}

// Positive: adoption on github.com writes the review-branch push and no AGit row; after the
// repository moves to a Forgejo host and declares forge: forgejo, a plain adopt refreshes that
// unmodified harness to the AGit push (#321).
func TestAdoptHarness_Positive_ForgeSelectsPushRows(t *testing.T) {
	repo, harness := adoptedGoWidget(t)
	rules := mustRead(t, filepath.Join(repo, paperclipRulesFile))
	if strings.Contains(harness, agitRow) || strings.Contains(rules, agitRow) || strings.Contains(rules, "AGit") ||
		!strings.Contains(harness, `"push_format"`) || !strings.Contains(rules, reviewBranchRow) {
		t.Fatalf("GitHub harness must carry only the review-branch push:\n%s\n%s", harness, rules)
	}
	writeOriginRemote(t, repo, "https://forgejo.example.org/acme/widget.git")
	declareForge(t, repo, "forgejo")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	harness = mustRead(t, filepath.Join(repo, paperclipFile))
	rules = mustRead(t, filepath.Join(repo, paperclipRulesFile))
	if !strings.Contains(harness, `"agit_push_format"`) || strings.Contains(harness, `"push_format"`) ||
		!strings.Contains(rules, "## AGit Push Protocol") || !strings.Contains(rules, agitRow) {
		t.Fatalf("Forgejo harness must carry the AGit push:\n%s\n%s", harness, rules)
	}
}

// Negative: a repository whose origin remote names a host other than github.com and whose
// manifest declares no forge gets no harness, and the skip names repository.forge; a declared
// manifest is then not bound to a harness that does not exist.
func TestAdoptHarness_Negative_UndeclaredForgeWritesNoHarness(t *testing.T) {
	repo := newTestRepo(t, "widget")
	writeOriginRemote(t, repo, "https://forgejo.example.org/acme/widget.git")
	mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false))
	if err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(repo, paperclipFile)) {
		t.Fatal("no harness may be written without the forge its push rows follow")
	}
	if !reportNames(report, paperclipFile, config.ForgeKey) {
		t.Fatalf("the paperclip skip must name %s: %+v", config.ForgeKey, report.ActionDetails)
	}
	if !reportNames(report, manifestFile, config.ForgeKey+" is undeclared") {
		t.Fatalf("the manifest note must say why register.sources is unbound: %+v", report.ActionDetails)
	}
}

// Boundary: a GitHub harness an earlier release wrote with the AGit push is unmodified earlier
// output, so a plain adopt refreshes it to the review-branch push, and an explicit forge: github
// on a non-GitHub host behaves as github.com does.
func TestAdoptHarness_Boundary_EarlierAGitHarnessRefreshed(t *testing.T) {
	repo, harness := adoptedGoWidget(t)
	path := filepath.Join(repo, paperclipFile)
	// The members are encoded as json.MarshalIndent wrote every released harness, < > & escaped.
	current, err := json.Marshal(reviewBranchRow)
	if err != nil {
		t.Fatal(err)
	}
	released, err := json.Marshal("git push origin HEAD:refs/for/main -o topic=<issue-id> && " + reviewBranchRow)
	if err != nil {
		t.Fatal(err)
	}
	earlier := strings.Replace(harness, `"push_format": `+string(current), `"agit_push_format": `+string(released), 1)
	if earlier == harness {
		t.Fatalf("fixture harness has no push_format row:\n%s", harness)
	}
	mustWrite(t, path, earlier)
	// rules.md of the earlier harness would carry its AGit section; an absent one is accepted
	// beside any earlier harness and stays absent (PriorState.Rules).
	if err := os.Remove(filepath.Join(repo, paperclipRulesFile)); err != nil {
		t.Fatal(err)
	}
	writeOriginRemote(t, repo, "https://code.example.org/acme/widget.git")
	declareForge(t, repo, "github")
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repo, false)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != harness {
		t.Fatalf("earlier AGit harness not refreshed to the GitHub synthesis:\n%s", got)
	}
}

// reportNames reports whether an action on path mentions text.
func reportNames(report *AdoptReport, path, text string) bool {
	for _, detail := range report.ActionDetails {
		if detail.Path == path && strings.Contains(detail.Details, text) {
			return true
		}
	}
	return false
}
