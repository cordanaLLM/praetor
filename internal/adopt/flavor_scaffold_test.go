package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flavorSession is an adoption session over a fresh directory holding files.
func flavorSession(t *testing.T, dryRun bool, files map[string]string) *adoptSession {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &adoptSession{repoPath: root, opts: AdoptOptions{DryRun: dryRun}, report: &AdoptReport{}}
}

// gitFlavorSession is flavorSession in a Git work tree, for a flavor whose requirement asks Git
// what the repository commits.
func gitFlavorSession(t *testing.T, files map[string]string) *adoptSession {
	t.Helper()
	requireGit(t)
	s := flavorSession(t, false, files)
	initTestGit(t, s.repoPath)
	return s
}

func scaffoldedCI(t *testing.T, s *adoptSession) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(s.repoPath, ".github", "workflows", "ci.yml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat scaffolded CI workflow: %v", err)
	}
	return err == nil
}

// goLibrary is the smallest tree flavor detection names go-library.
var goLibrary = map[string]string{"go.mod": "module example.com/widget\n\ngo 1.27\n", "internal/w/w.go": "package w\n"}

// Positive: a detected flavor is scaffolded, CI workflow included, and nothing is warned.
func TestReconcileWorkingDirAndFlavor_Positive_DetectedFlavorIsScaffolded(t *testing.T) {
	s := flavorSession(t, false, goLibrary)
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if !scaffoldedCI(t, s) {
		t.Fatalf("the detected go-library flavor scaffolded no CI workflow; report %+v", s.report)
	}
	if len(s.report.Warnings) != 0 || len(s.report.Errors) != 0 {
		t.Fatalf("unexpected warnings %v errors %v", s.report.Warnings, s.report.Errors)
	}
}

// Negative: nothing detected scaffolds nothing and says so. This used to apply go-library,
// whose CI job then became a required check the repository could never pass.
func TestReconcileWorkingDirAndFlavor_Negative_UndetectedRepositoryGetsNoFallbackFlavor(t *testing.T) {
	s := flavorSession(t, false, map[string]string{"README.md": "# docs only\n"})
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if scaffoldedCI(t, s) {
		t.Fatal("an undetected repository received the fallback flavor's CI workflow")
	}
	if _, err := os.Stat(filepath.Join(s.repoPath, ".golangci.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an undetected repository received the fallback flavor's linter config: %v", err)
	}
	if len(s.report.Warnings) != 1 || !strings.Contains(s.report.Warnings[0], "no flavor matched") {
		t.Fatalf("want one no-flavor warning, got %v", s.report.Warnings)
	}
}

// Boundary: a dry run writes no flavor file even when a flavor is detected.
func TestReconcileWorkingDirAndFlavor_Boundary_DryRunWritesNothing(t *testing.T) {
	s := flavorSession(t, true, goLibrary)
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if scaffoldedCI(t, s) {
		t.Fatal("a dry run scaffolded the CI workflow")
	}
}

// Negative: a detected flavor holds back a template whose body cannot work here, and adoption
// says which. The review's probe: a Go service whose package.json only carries commit tooling,
// locked by pnpm, detects typescript-node, whose CI job runs `npm ci` and `npm test`.
func TestReconcileWorkingDirAndFlavor_Negative_UnrunnableNodeJobIsWithheldAndWarned(t *testing.T) {
	s := flavorSession(t, false, map[string]string{
		"go.mod": "module example.com/widget\n\ngo 1.27\n", "cmd/widget/main.go": "package main\n\nfunc main() {}\n",
		"internal/w/w.go": "package w\n", "pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
		"package.json": `{"private": true, "devDependencies": {"@commitlint/cli": "^20.0.0"}}`,
	})
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if scaffoldedCI(t, s) {
		t.Fatal("adoption scaffolded an npm CI job into a repository with no package-lock.json")
	}
	if len(s.report.Warnings) != 1 || !strings.Contains(s.report.Warnings[0], "typescript-node did not scaffold .github/workflows/ci.yml") ||
		!strings.Contains(s.report.Warnings[0], "package-lock.json") {
		t.Fatalf("want one warning naming the withheld CI job and its missing lockfile, got %v", s.report.Warnings)
	}
}

// npmProjectFiles is an npm project whose CI job can pass once Git commits its lockfile.
func npmProjectFiles() map[string]string {
	return map[string]string{
		"package.json":      `{"name": "widget", "scripts": {"test": "node --test"}}`,
		"package-lock.json": `{"name": "widget", "lockfileVersion": 3, "requires": true, "packages": {"": {"name": "widget"}}}`,
	}
}

// Negative: a library that git-ignores package-lock.json, while a local `npm install` has
// written one, gets no npm CI job: CI's checkout holds no lockfile, so the required check would
// fail on every pull request. Adoption warns instead.
func TestReconcileWorkingDirAndFlavor_Negative_IgnoredLockfileWithholdsTheNodeJob(t *testing.T) {
	files := npmProjectFiles()
	files[".gitignore"] = "node_modules/\npackage-lock.json\n"
	s := gitFlavorSession(t, files)
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if scaffoldedCI(t, s) {
		t.Fatal("adoption scaffolded an npm CI job whose lockfile Git never commits")
	}
	if len(s.report.Warnings) != 1 || !strings.Contains(s.report.Warnings[0], "package-lock.json is untracked and git-ignored") {
		t.Fatalf("want one warning naming the ignored lockfile, got %v", s.report.Warnings)
	}
}

// Boundary: an npm project that meets the job's requirements gets it, and no warning.
func TestReconcileWorkingDirAndFlavor_Boundary_NpmProjectGetsTheNodeJob(t *testing.T) {
	s := gitFlavorSession(t, npmProjectFiles())
	if err := reconcileWorkingDirAndFlavor(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if !scaffoldedCI(t, s) {
		t.Fatalf("an npm project received no CI job; report %+v", s.report)
	}
	if len(s.report.Warnings) != 0 || len(s.report.Errors) != 0 {
		t.Fatalf("unexpected warnings %v errors %v", s.report.Warnings, s.report.Errors)
	}
}
