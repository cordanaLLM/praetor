package main

import (
	"strings"
	"testing"
)

const dedupeSprawlSource = `package sample

import "os/exec"

func CallGit() {
	_ = exec.Command("git", "status")
}
`

const dedupeCleanSource = `package sample

func Add(a, b int) int {
	sum := a + b
	return sum
}
`

// dedupeFixtureDir puts one source file into a fresh directory and returns the directory.
// The write itself is writeFixtureFile (g02_helpers_test.go), the package's one fixture
// writer; this adds only the t.TempDir() every caller here wants (HISS-19).
func dedupeFixtureDir(t *testing.T, name, source string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, name, source)
	return dir
}

// TestRunDedupeScan_Negative_FailureNamesTheFindings pins what the scan says when it fails.
//
// The message used to be "dedupe audit failed with score 95.0%", which reads like a
// threshold the repository missed; the verdict is the finding lists, so a single sprawl item
// fails the scan at a score of 95 and the reader needs the count, not the percentage.
func TestRunDedupeScan_Negative_FailureNamesTheFindings(t *testing.T) {
	dir := dedupeFixtureDir(t, "sprawl.go", dedupeSprawlSource)

	err := runDedupeScan([]string{dir})
	if err == nil {
		t.Fatal("a repository with an unresolved sprawl finding must fail the scan")
	}
	for _, want := range []string{"1 utility sprawl finding", "0 duplicate function group"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "failed with score") {
		t.Errorf("error = %q, want the counts rather than a threshold reading", err)
	}
}

// TestRunDedupeScan_Positive_CleanRepositoryPasses checks that the stricter verdict did not
// turn every scan into a failure.
func TestRunDedupeScan_Positive_CleanRepositoryPasses(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)

	if err := runDedupeScan([]string{dir}); err != nil {
		t.Fatalf("a clean repository must pass: %v", err)
	}
}

// TestRunDedupeScan_Boundary_NoGoSourcesIsNotAVerdict covers the applicability guard: a tree
// the detector never opened is neither a pass nor a failure, so the command returns nil
// without claiming the repository is clean.
func TestRunDedupeScan_Boundary_NoGoSourcesIsNotAVerdict(t *testing.T) {
	dir := dedupeFixtureDir(t, "README.md", "# not go\n")

	if err := runDedupeScan([]string{dir}); err != nil {
		t.Fatalf("a repository with no Go sources must not be reported as failing: %v", err)
	}
}
