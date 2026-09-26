package dedupe_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
)

// scratchSource is long enough to be hashed as a clone candidate.
const scratchSource = `package test
import "fmt"
func Process(x int) int {
	fmt.Println("step 1")
	fmt.Println("step 2")
	fmt.Println("step 3")
	fmt.Println("step 4")
	return x * 2
}
`

// duplicatesWithCopyUnder writes scratchSource at the root of a non-Git tree and again under
// copyDir, and returns the number of duplicate groups the directory walk reports.
func duplicatesWithCopyUnder(t *testing.T, copyDir string) int {
	t.Helper()
	tmp := t.TempDir()
	for _, rel := range []string{"a.go", filepath.Join(copyDir, "a.go")} {
		path := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(scratchSource), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := dedupe.ScanRepo(tmp)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return len(report.Duplicates)
}

// TestScanRepo_Positive_ScratchCopiesAreNotClones: a .claude/worktrees or .standards copy of the
// checkout is the same source again, not a clone to report.
func TestScanRepo_Positive_ScratchCopiesAreNotClones(t *testing.T) {
	for _, dir := range []string{".claude/worktrees/agent-1", ".standards/cache"} {
		if got := duplicatesWithCopyUnder(t, dir); got != 0 {
			t.Fatalf("copy under %s reported %d duplicate group(s)", dir, got)
		}
	}
}

// TestScanRepo_Negative_OrdinaryDirectoryCopyIsAClone: the skip is by scratch name only.
func TestScanRepo_Negative_OrdinaryDirectoryCopyIsAClone(t *testing.T) {
	if got := duplicatesWithCopyUnder(t, "claude/worktrees"); got != 1 {
		t.Fatalf("an ordinary copy must be reported as one duplicate group, got %d", got)
	}
}

// TestScanRepo_Boundary_EveryLedgerDirectoryIsSkipped: both private ledgers are scratch, not
// only the first one the old list named.
func TestScanRepo_Boundary_EveryLedgerDirectoryIsSkipped(t *testing.T) {
	for _, dir := range []string{".workingdir", ".workingdir2"} {
		if got := duplicatesWithCopyUnder(t, dir); got != 0 {
			t.Fatalf("copy under %s reported %d duplicate group(s)", dir, got)
		}
	}
}
