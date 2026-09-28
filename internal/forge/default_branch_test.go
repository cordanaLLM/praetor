// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// defaultBranchRepo initialises a checkout whose origin HEAD names originHead, or records none
// when originHead is empty, or skips the test when git is unavailable (HISS-21: the origin HEAD is
// read through git, and a host without git cannot answer).
func defaultBranchRepo(t *testing.T, originHead string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	root := t.TempDir()
	if _, err := util.RunGit(t.Context(), root, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if originHead != "" {
		if _, err := util.RunGit(t.Context(), root, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/"+originHead); err != nil {
			t.Fatalf("record origin HEAD: %v", err)
		}
	}
	return root
}

func mustDefaultBranch(t *testing.T, root string, manifest *config.Manifest) string {
	t.Helper()
	branch, err := RepositoryDefaultBranch(t.Context(), root, manifest)
	if err != nil {
		t.Fatalf("resolve the default branch of %s: %v", root, err)
	}
	return branch
}

// main, master and a custom name each come out of the source that names them, and the ruleset
// protects that branch and lts-*.
func TestRepositoryDefaultBranch_Positive_MainMasterAndACustomName(t *testing.T) {
	if got := mustDefaultBranch(t, defaultBranchRepo(t, "main"), nil); got != "main" {
		t.Fatalf("origin HEAD at main resolved %q", got)
	}
	if got := mustDefaultBranch(t, defaultBranchRepo(t, "master"), nil); got != "master" {
		t.Fatalf("origin HEAD at master resolved %q", got)
	}
	custom := defaultBranchRepo(t, "")
	writeManifestFixture(t, custom, "version: 1\nrepository:\n  default_branch: release/stable\n")
	if got := mustDefaultBranch(t, custom, nil); got != "release/stable" {
		t.Fatalf("a declared custom branch resolved %q", got)
	}
	if refs := RepositoryRulesetRefs("master"); !slices.Equal(refs, []string{"refs/heads/master", "refs/heads/lts-*"}) {
		t.Fatalf("ruleset refs for master = %v", refs)
	}
}

// The offline order: a declaration beats the origin HEAD, which beats main; a manifest the caller
// loaded beats the one on disk, and a checkout recording nothing and declaring nothing is main.
func TestRepositoryDefaultBranch_Boundary_FallbackOrder(t *testing.T) {
	root := defaultBranchRepo(t, "master")
	writeManifestFixture(t, root, "version: 1\nrepository:\n  default_branch: trunk\n")
	if got := mustDefaultBranch(t, root, nil); got != "trunk" {
		t.Fatalf("the declaration must beat origin HEAD, got %q", got)
	}
	loaded := &config.Manifest{Repository: config.RepositoryMetadata{DefaultBranch: "develop"}}
	if got := mustDefaultBranch(t, root, loaded); got != "develop" {
		t.Fatalf("the caller's manifest must beat the one on disk, got %q", got)
	}
	if got := mustDefaultBranch(t, root, &config.Manifest{}); got != "master" {
		t.Fatalf("a manifest declaring nothing must leave origin HEAD, got %q", got)
	}
	writeManifestFixture(t, root, "version: 1\n")
	if got := mustDefaultBranch(t, root, nil); got != "master" {
		t.Fatalf("origin HEAD must beat main, got %q", got)
	}
	if got := mustDefaultBranch(t, defaultBranchRepo(t, ""), nil); got != FallbackDefaultBranch {
		t.Fatalf("no declaration and no origin HEAD must be %s, got %q", FallbackDefaultBranch, got)
	}
	if got := mustDefaultBranch(t, t.TempDir(), nil); got != FallbackDefaultBranch {
		t.Fatalf("a directory outside any checkout must be %s, got %q", FallbackDefaultBranch, got)
	}
	long := strings.Repeat("b", 128)
	if got := mustDefaultBranch(t, root, &config.Manifest{Repository: config.RepositoryMetadata{DefaultBranch: long}}); got != long {
		t.Fatalf("a 128-character branch must resolve, got %q", got)
	}
}

// A malformed declaration, a manifest that does not load, an origin HEAD naming a branch the
// ruleset cannot carry and a missing or cancelled context are errors, never a fall through to
// the next source.
func TestRepositoryDefaultBranch_Negative_UnusableSourcesAreErrors(t *testing.T) {
	root := defaultBranchRepo(t, "master")
	for _, declared := range []string{"feature..x", "-dash", strings.Repeat("b", 129)} {
		manifest := &config.Manifest{Repository: config.RepositoryMetadata{DefaultBranch: declared}}
		if got, err := RepositoryDefaultBranch(t.Context(), root, manifest); err == nil {
			t.Fatalf("declared %q must be refused, resolved %q", declared, got)
		}
	}
	broken := defaultBranchRepo(t, "master")
	writeManifestFixture(t, broken, "version: 1\nrepository:\n  default_branch: [main]\n")
	if got, err := RepositoryDefaultBranch(t.Context(), broken, nil); err == nil {
		t.Fatalf("a manifest that does not load must be an error, resolved %q", got)
	}
	if got, err := RepositoryDefaultBranch(t.Context(), defaultBranchRepo(t, "_private"), nil); err == nil ||
		!strings.Contains(err.Error(), "repository.default_branch") {
		t.Fatalf("an origin HEAD the ruleset cannot carry must ask for a declaration, got %q, %v", got, err)
	}
	var missing context.Context
	if _, err := RepositoryDefaultBranch(missing, root, nil); err == nil {
		t.Fatal("a nil context must be refused")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := RepositoryDefaultBranch(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled origin HEAD read must be an error, got %v", err)
	}
}

// The rendering follows the default branch; the audit's validation refuses a ruleset rendered
// for another branch, and a branch name the ruleset cannot carry is not rendered.
func TestRenderRulesetForRepository_Positive_ProtectsTheDefaultBranch(t *testing.T) {
	root := defaultBranchRepo(t, "master")
	policy := config.DefaultPolicy().BranchProtection
	data, contexts, err := RenderRulesetForRepository(t.Context(), root, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"refs/heads/master"`) || strings.Contains(string(data), `"refs/heads/main"`) {
		t.Fatalf("a master repository's ruleset must protect master alone besides lts-*:\n%s", data)
	}
	if err := ValidateRepositoryRuleset(data, "master", policy, contexts); err != nil {
		t.Fatalf("the rendering must validate for its branch: %v", err)
	}
	legacy, err := RenderRepositoryRuleset(FallbackDefaultBranch, policy, contexts)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRepositoryRuleset(legacy, "master", policy, contexts); !errors.Is(err, ErrRulesetDrift) {
		t.Fatalf("a main ruleset in a master repository must be drift, got %v", err)
	}
	for _, branch := range []string{"", "a..b", "_x"} {
		if _, err := RenderRepositoryRuleset(branch, policy, nil); err == nil {
			t.Fatalf("branch %q must not be rendered", branch)
		}
	}
}

// The baseline records the default branch. A master repository's earlier main rendering, which
// every Praetor before this one wrote, is a prior rendering as much as the master one before the
// run; a main repository has one prior rendering only, and none that is current.
func TestPriorRulesetDigests_Boundary_TheEarlierMainRenderingIsRecognised(t *testing.T) {
	root := defaultBranchRepo(t, "master")
	baseline, err := ReadRulesetBaseline(t.Context(), root)
	if err != nil || baseline.Branch != "master" {
		t.Fatalf("baseline of a master repository = %+v, %v", baseline, err)
	}
	signed := baseline.Policy
	signed.RequireSignedCommits = true
	current, err := RenderRepositoryRuleset("master", signed, baseline.Contexts)
	if err != nil {
		t.Fatal(err)
	}
	priors := PriorRulesetDigests(baseline, current)
	for _, branch := range []string{"master", FallbackDefaultBranch} {
		prior, err := RenderRepositoryRuleset(branch, baseline.Policy, baseline.Contexts)
		if err != nil {
			t.Fatal(err)
		}
		if _, known, _ := util.LookupCanonicalText(prior, priors); !known {
			t.Fatalf("the %s rendering before the run must be a prior rendering: %v", branch, priors)
		}
	}
	mainBaseline := RulesetBaseline{Branch: FallbackDefaultBranch, Policy: baseline.Policy}
	legacyCurrent, err := RenderRepositoryRuleset(FallbackDefaultBranch, baseline.Policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := PriorRulesetDigests(mainBaseline, legacyCurrent); got != nil {
		t.Fatalf("a main repository whose rendering is current has no prior rendering, got %v", got)
	}
	if got := PriorRulesetDigests(mainBaseline, current); len(got) != 1 {
		t.Fatalf("a main repository has exactly one prior rendering, got %v", got)
	}
}
