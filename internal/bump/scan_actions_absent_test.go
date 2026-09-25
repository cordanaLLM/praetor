package bump

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanWorkflowActionsDistinguishesAbsentFromMisplaced covers both sides of treating a
// missing workflow directory as "no workflows". On Windows, reading a directory whose path is
// a regular file reports not-exist, so a misplaced .github/workflows file used to be read as an
// empty scan there while POSIX refused it.
func TestScanWorkflowActionsDistinguishesAbsentFromMisplaced(t *testing.T) {
	absent := t.TempDir()
	if got, warnings, err := ScanWorkflowActions(t.Context(), absent); err != nil || len(got) != 0 || len(warnings) != 0 {
		t.Fatalf("absent workflow directory was not an empty scan: %v %v %v", got, warnings, err)
	}
	for _, file := range []string{".github", filepath.Join(".github", "workflows")} {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, file)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, file), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ScanWorkflowActions(t.Context(), repo); err == nil {
			t.Fatalf("a regular file at %s was read as an empty workflow directory", file)
		}
	}
}
