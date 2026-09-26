package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/util"
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

// cadenceFixture returns a repository whose last dedupe sweep is recorded at its first commit
// and whose second commit adds twelve lines of Go production source.
func cadenceFixture(t *testing.T) string {
	t.Helper()
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	git := func(args ...string) {
		t.Helper()
		if out, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := []string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m"}
	git("init", "-q")
	git("add", "-A", ".")
	git(append(commit, "init")...)
	if err := dedupe.RecordCadence(t.Context(), dir, 20); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, "grow.go", "package sample\n"+strings.Repeat("var _ = 1\n", 11))
	git("add", "-A", ".")
	git(append(commit, "grow")...)
	return dir
}

// The cadence command used to decide on commits alone; the growth thresholds are flags, and
// the trigger it fires on is named in its output.
func TestRunDedupeCadence_Positive_GrowthTriggersTheSweep(t *testing.T) {
	dir := cadenceFixture(t)
	out, err := captureStdout(t, func() error { return runDedupeCadence([]string{"--added-lines=10", dir}) })
	if err != nil {
		t.Fatalf("cadence: %v\n%s", err, out)
	}
	for _, want := range []string{"12 Go source lines", "trigger: true", "Triggering periodic codebase deduplication audit: 12 Go source lines added"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRunDedupeCadence_Negative_RejectsMalformedThreshold(t *testing.T) {
	if err := runDedupeCadence([]string{"--added-lines=many", t.TempDir()}); err == nil {
		t.Fatal("a non-numeric growth threshold must be refused")
	}
}

func TestRunDedupeCadence_Boundary_GrowthBelowThresholdStaysQuiet(t *testing.T) {
	dir := cadenceFixture(t)
	out, err := captureStdout(t, func() error { return runDedupeCadence([]string{"--added-lines=13", dir}) })
	if err != nil {
		t.Fatalf("cadence: %v\n%s", err, out)
	}
	if !strings.Contains(out, "trigger: false") || strings.Contains(out, "Triggering") {
		t.Fatalf("12 added lines against a 13-line threshold must not trigger a sweep:\n%s", out)
	}
}
