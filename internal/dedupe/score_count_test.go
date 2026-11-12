package dedupe

import (
	"context"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// TestScoreCountsSmallFunctions covers functions below the clone detection threshold.
func TestScoreCountsSmallFunctions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte("package sample\nfunc a() {}\nfunc b() { println(1) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := &DedupeReport{}
	var starts []adopterGitStart
	err := scanGoFile(context.Background(), token.NewFileSet(), path, "sample.go", map[string][]FileLocation{}, map[string]int{}, false, &starts, report)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalFuncsScanned != 2 {
		t.Fatalf("scanned %d functions, want 2", report.TotalFuncsScanned)
	}
}
