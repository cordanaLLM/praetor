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
