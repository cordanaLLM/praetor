package dedupe_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestScanRepo_Positive_DuplicatesAndSprawl(t *testing.T) {
	tmp := t.TempDir()

	file1 := `package test
import "fmt"
func ProcessDataA(x int) int {
	fmt.Println("step 1")
	fmt.Println("step 2")
	fmt.Println("step 3")
	fmt.Println("step 4")
	return x * 2
}
`
	file2 := `package test
import "fmt"
func ProcessDataB(x int) int {
	fmt.Println("step 1")
	fmt.Println("step 2")
	fmt.Println("step 3")
	fmt.Println("step 4")
	return x * 2
}
`
	sprawlFile := `package test
import "os/exec"
func CallGit() {
	_ = exec.Command("git", "status")
}
`
	if err := os.WriteFile(filepath.Join(tmp, "a.go"), []byte(file1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.go"), []byte(file2), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "c.go"), []byte(sprawlFile), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := dedupe.ScanRepo(tmp)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}

	if len(report.Duplicates) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d", len(report.Duplicates))
	}
	if len(report.Duplicates[0].Locations) != 2 {
		t.Fatalf("expected 2 locations in duplicate group, got %d", len(report.Duplicates[0].Locations))
	}
	if len(report.SprawlItems) != 1 {
		t.Fatalf("expected 1 sprawl item, got %d", len(report.SprawlItems))
	}
	if report.Passed {
		t.Fatal("expected report to fail due to duplicates and sprawl")
	}
}

func TestScanRepo_Boundary_SmallFunctionsIgnored(t *testing.T) {
	tmp := t.TempDir()
	// Trivial 2-line functions should not trigger duplicate detection
	file1 := "package test\nfunc GetA() int {\n\treturn 1\n}\n"
	file2 := "package test\nfunc GetB() int {\n\treturn 1\n}\n"

	if err := os.WriteFile(filepath.Join(tmp, "a.go"), []byte(file1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "b.go"), []byte(file2), 0644); err != nil {
		t.Fatal(err)
	}

	report, err := dedupe.ScanRepo(tmp)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}
	if len(report.Duplicates) != 0 {
		t.Fatalf("expected 0 duplicates for small functions, got %d", len(report.Duplicates))
	}
	if !report.Passed {
		t.Fatal("expected clean repo to pass")
	}
}

func TestCadence_PositiveAndBoundary(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()

	// Initialize git repo
	_, err := util.RunGit(ctx, tmp, "init")
	if err != nil {
		t.Skip("git not available or init failed")
	}
	_, _ = util.RunGit(ctx, tmp, "config", "user.email", "test@cordana.ai")
	_, _ = util.RunGit(ctx, tmp, "config", "user.name", "Praetor Test")

	// Commit 1
	_ = os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Test\n"), 0644)
	_, _ = util.RunGit(ctx, tmp, "add", "README.md")
	_, _ = util.RunGit(ctx, tmp, "commit", "-m", "init")

	// 1. Initial check: should run because never recorded
	shouldRun, delta, err := dedupe.CheckCadence(ctx, tmp, 5)
	if err != nil {
		t.Fatalf("check cadence failed: %v", err)
	}
	if !shouldRun || delta != 1 {
		t.Fatalf("expected shouldRun=true, delta=1, got shouldRun=%v, delta=%d", shouldRun, delta)
	}

	// 2. Record cadence
	if err := dedupe.RecordCadence(ctx, tmp, 5); err != nil {
		t.Fatalf("record cadence failed: %v", err)
	}

	// 3. Check again immediately: delta should be 0, shouldRun=false
	shouldRun2, delta2, err := dedupe.CheckCadence(ctx, tmp, 5)
	if err != nil {
		t.Fatalf("check cadence failed: %v", err)
	}
	if shouldRun2 || delta2 != 0 {
		t.Fatalf("expected shouldRun=false, delta=0, got shouldRun=%v, delta=%d", shouldRun2, delta2)
	}
}

func TestCadence_Negative_NonGitDir(t *testing.T) {
	tmp := t.TempDir()
	ctx := context.Background()
	shouldRun, delta, err := dedupe.CheckCadence(ctx, tmp, 10)
	if err != nil {
		t.Fatalf("unexpected error for non-git dir: %v", err)
	}
	if shouldRun || delta != 0 {
		t.Fatalf("expected shouldRun=false, delta=0, got %v, %d", shouldRun, delta)
	}
}
