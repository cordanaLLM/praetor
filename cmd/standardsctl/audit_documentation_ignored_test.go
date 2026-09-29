package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// untrackFigureEngine drops the committed figure engine dist files from the index, so they sit
// on disk untracked, as an adoption leaves them before the operator commits.
func untrackFigureEngine(t *testing.T, root string) {
	t.Helper()
	if out, err := runFixtureGit(t, root, testsupport.HermeticGitEnv(t), "rm", "-q", "-r", "--cached", "tools/figures/dist"); err != nil {
		t.Skipf("git rm --cached failed in sandbox: %v (%s)", err, out)
	}
}

func docsManifest() *config.Manifest {
	return &config.Manifest{Facets: []string{"docs:seo-portal"}}
}

// Negative: a documentation gate file that exists on disk but that git ignores and does not
// track fails the gate locally, as it fails a clean checkout, and the failure names each file,
// the rule and the negation that re-includes it.
func TestAuditDocumentationGate_Negative_IgnoredUntrackedFileFails(t *testing.T) {
	root := documentationAuditFixture(t)
	untrackFigureEngine(t, root)
	writeFixtureFile(t, root, ".gitignore", "dist/\n\n"+adopt.ManagedGitIgnoreBlock())
	err := docGate(t.Context(), docsManifest(), root)
	want := "tools/figures/dist/loader.js (ignored by .gitignore:1 (dist/); add !/tools/figures/dist/ after that rule)"
	if err == nil || !strings.Contains(err.Error(), "[FAIL] Documentation gate files are ignored by git and not tracked") ||
		!strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "tools/figures/dist/player.js") {
		t.Fatalf("ignored figure engine passed or was not named: %v", err)
	}
}

// Positive: the same rule passes once the files are tracked, whose changes git commits
// whatever the rule says, and passes untracked once a negation re-includes the directory.
func TestAuditDocumentationGate_Positive_TrackedOrReincludedFilesPass(t *testing.T) {
	root := documentationAuditFixture(t)
	writeFixtureFile(t, root, ".gitignore", "dist/\n\n"+adopt.ManagedGitIgnoreBlock())
	if err := docGate(t.Context(), docsManifest(), root); err != nil {
		t.Fatalf("tracked files under an ignore rule failed: %v", err)
	}
	untrackFigureEngine(t, root)
	writeFixtureFile(t, root, ".gitignore", "dist/\n!/tools/figures/dist/\n\n"+adopt.ManagedGitIgnoreBlock())
	if err := docGate(t.Context(), docsManifest(), root); err != nil {
		t.Fatalf("re-included files failed: %v", err)
	}
}

// Boundary: the managed block adoption writes under a Kconfig-style rule, with !/.config/ as
// its last rule, is canonical; a block with any other extra rule is not.
func TestAuditDocumentationGate_Boundary_ConfigNegationBlockIsCanonical(t *testing.T) {
	root := documentationAuditFixture(t)
	block := adopt.ManagedGitIgnoreBlock()
	end := "# END praetor private artifacts\n"
	writeFixtureFile(t, root, ".gitignore", ".config\n\n"+strings.Replace(block, end, "!/.config/\n"+end, 1))
	if err := docGate(t.Context(), docsManifest(), root); err != nil {
		t.Fatalf("the block with the .config negation failed: %v", err)
	}
	writeFixtureFile(t, root, ".gitignore", strings.Replace(block, end, "!docs/\n"+end, 1))
	if err := docGate(t.Context(), docsManifest(), root); err == nil ||
		!strings.Contains(err.Error(), "canonical Praetor private-artifact block") {
		t.Fatalf("a block with a foreign rule passed: %v", err)
	}
}
