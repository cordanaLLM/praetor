package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fillEntries writes count empty files under dir.
func fillEntries(t *testing.T, dir string, count int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestVerificationWalk_Positive_ScratchWorktreesDoNotCountAgainstTheBound: a checkout whose
// .claude/worktrees and .standards trees alone exceed the entry bound still plans, because
// neither holds the repository's own verification input.
func TestVerificationWalk_Positive_ScratchWorktreesDoNotCountAgainstTheBound(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/m\n")
	fillEntries(t, filepath.Join(root, ".claude", "worktrees", "agent-1"), fixtureEntries+1)
	fillEntries(t, filepath.Join(root, ".standards", "cache"), fixtureEntries+1)
	inputs, err := loadVerificationInputsWithLimits(t.Context(), root, *withEntries(fixtureEntries))
	if err != nil {
		t.Fatalf("scratch trees must not exhaust the verification bound: %v", err)
	}
	if !inputs.has("go.mod") {
		t.Fatal("the repository's own marker was not captured")
	}
}

// TestVerificationWalk_Negative_OrdinaryDirectoryStillHitsTheBound: the skip is by name, not a
// loosened bound; the same tree under any other directory still fails.
func TestVerificationWalk_Negative_OrdinaryDirectoryStillHitsTheBound(t *testing.T) {
	root := t.TempDir()
	fillEntries(t, filepath.Join(root, "claude", "worktrees"), fixtureEntries+1)
	if _, err := loadVerificationInputsWithLimits(t.Context(), root, *withEntries(fixtureEntries)); err == nil {
		t.Fatal("an ordinary directory past the entry bound was accepted")
	}
}

// TestVerificationWalk_Boundary_ScratchMarkersAreNotInputs: a marker inside a scratch copy is
// the checkout's own marker again, never a second project to verify.
func TestVerificationWalk_Boundary_ScratchMarkersAreNotInputs(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".claude", "worktrees", "a", "package.json"), "{}")
	mustWrite(t, filepath.Join(root, ".workingdir2", "Project.csproj"), "<Project/>")
	inputs, err := loadVerificationInputs(t.Context(), root)
	if err != nil || len(inputs.files) != 0 {
		t.Fatalf("scratch markers captured: %v, %v", inputs.files, err)
	}
}

// TestCatalogIgnore_Positive_BareConfigIgnoreIsReportedAndStillWritten is the kernel-tree
// shape with the git-ignore step not run (declined): a bare .config rule excludes
// .config/archetypes, and adoption says so for each file, names the rule and proposes the
// directory-only negation, rather than write files git silently drops.
func TestCatalogIgnore_Positive_BareConfigIgnoreIsReportedAndStillWritten(t *testing.T) {
	requireGit(t)
	s, policy := catalogSession(t)
	initTestGit(t, s.repoPath)
	mustWrite(t, filepath.Join(s.repoPath, ".gitignore"), ".config\n")
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	reportIgnoredWrites(t.Context(), s)
	if len(s.report.Errors) != len(policy.CatalogArtifacts) {
		t.Fatalf("want one error per ignored catalog file, got %q", s.report.Errors)
	}
	errors := strings.Join(s.report.Errors, "\n")
	for _, artifact := range policy.CatalogArtifacts {
		want := artifact.RelativePath + ": ignored by .gitignore:1 (.config); adoption writes it, but git will not commit it"
		if !strings.Contains(errors, want) || !strings.Contains(errors, "Add !/.config/ after that rule (directory-only") {
			t.Fatalf("%s not named with its rule and negation in %q", artifact.RelativePath, errors)
		}
		if _, err := os.Stat(filepath.Join(s.repoPath, artifact.RelativePath)); err != nil {
			t.Fatalf("the local audit still needs %s: %v", artifact.RelativePath, err)
		}
	}
}

// TestCatalogIgnore_Negative_UnrelatedIgnoreRulesStaySilent: rules that leave the catalog
// alone add neither an error nor a warning.
func TestCatalogIgnore_Negative_UnrelatedIgnoreRulesStaySilent(t *testing.T) {
	requireGit(t)
	s, _ := catalogSession(t)
	initTestGit(t, s.repoPath)
	mustWrite(t, filepath.Join(s.repoPath, ".gitignore"), "build/\n.config/local.yaml\n")
	warnings := len(s.report.Warnings)
	if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	reportIgnoredWrites(t.Context(), s)
	if len(s.report.Errors) != 0 || len(s.report.Warnings) != warnings {
		t.Fatalf("unrelated rules reported: errors %q, warnings %q", s.report.Errors, s.report.Warnings[warnings:])
	}
}

// TestCatalogIgnore_Boundary_UnanswerableGitIsAStatedSkip: without git, or outside a work tree,
// the check cannot run; that is a warning, never an error and never silence.
func TestCatalogIgnore_Boundary_UnanswerableGitIsAStatedSkip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T)
	}{
		{"no git on PATH", func(t *testing.T) { t.Setenv("PATH", t.TempDir()) }},
		{"not a work tree", func(t *testing.T) { requireGit(t) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := catalogSession(t)
			if err := reconcilePolicyCatalog(t.Context(), s); err != nil {
				t.Fatal(err)
			}
			tc.setup(t)
			warnings := len(s.report.Warnings)
			reportIgnoredWrites(t.Context(), s)
			added := strings.Join(s.report.Warnings[warnings:], "\n")
			if len(s.report.Errors) != 0 || !strings.Contains(added, "adopted files not checked against .gitignore") {
				t.Fatalf("want a stated skip, got errors %q, warnings %q", s.report.Errors, added)
			}
		})
	}
}
