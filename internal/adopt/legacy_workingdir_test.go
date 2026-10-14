package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

func legacyWarnings(t *testing.T, root string) []string {
	t.Helper()
	session := &adoptSession{repoPath: root, report: &AdoptReport{}}
	session.reportLegacyWorkingDir()
	return session.report.Warnings
}

// Positive (#1233): adoption over a populated .workingdir2/ says so and names the migrate
// command, instead of creating a stub ledger without a word.
func TestReportLegacyWorkingDir_Positive_PopulatedFolderWarns(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, state.LegacyWorkingDirName)
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "OPEN.md"), []byte("- [ ] legacy task\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := legacyWarnings(t, root)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "praetorctl state migrate") ||
		!strings.Contains(warnings[0], state.LegacyWorkingDirName+"/OPEN.md") {
		t.Fatalf("populated legacy folder not reported: %q", warnings)
	}
}

// Negative: a repository without the legacy folder gets no warning.
func TestReportLegacyWorkingDir_Negative_NoFolderNoWarning(t *testing.T) {
	if warnings := legacyWarnings(t, t.TempDir()); len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %q", warnings)
	}
}

// Boundary: an empty legacy folder holds nothing to migrate and is not reported.
func TestReportLegacyWorkingDir_Boundary_EmptyFolderNoWarning(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, state.LegacyWorkingDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if warnings := legacyWarnings(t, root); len(warnings) != 0 {
		t.Fatalf("empty legacy folder reported: %q", warnings)
	}
}
