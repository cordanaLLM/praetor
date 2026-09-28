package main

import (
	"encoding/json"
	"errors"
	"flag"
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

// #571: `dedupe scan` took args[0] as the directory and dropped the rest, so -h failed as a
// missing directory, `--json .` scanned a directory named --json, and `. --json` ignored the
// flag. It now parses its arguments like every other subcommand.

func TestRunDedupeScan_Positive_HelpTokensPrintUsageAndSucceed(t *testing.T) {
	for _, tok := range []string{"-h", "--help", "help"} {
		out, err := captureStdout(t, func() error { return runDedupeScan([]string{tok}) })
		if err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("dedupe scan %s must be answered as help, got %v", tok, err)
		}
		mustContain(t, out, "Usage: praetorctl dedupe scan", "-json")
	}
	if err := runDedupeScan([]string{t.TempDir(), "-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("a trailing -h must be answered as help, got %v", err)
	}
}

func TestRunDedupeScan_Positive_JSONFlagAnywhereInArgv(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	for _, args := range [][]string{{"--json", dir}, {dir, "--json"}, {"--json", "--path", dir}} {
		out, err := captureStdout(t, func() error { return runDedupeScan(args) })
		if err != nil {
			t.Fatalf("dedupe scan %q: %v", args, err)
		}
		var report dedupe.DedupeReport
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("dedupe scan %q must print JSON: %v\n%s", args, err, out)
		}
		if report.TotalFilesScanned != 1 || !report.Passed {
			t.Errorf("dedupe scan %q: report = %+v, want one clean file", args, report)
		}
	}
}

func TestRunDedupeScan_Negative_UnknownFlagAndSecondDirectoryAreRejected(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	if err := runDedupeScan([]string{"--bogus", dir}); err == nil || !strings.Contains(err.Error(), "-bogus") {
		t.Errorf("an unknown flag must be refused, got %v", err)
	}
	mustErrContain(t, runDedupeScan([]string{dir, dir}), "at most one directory")
}

// The JSON form keeps the text form's exit, and "--" still passes a dash-led directory.
func TestRunDedupeScan_Boundary_JSONKeepsTheVerdict(t *testing.T) {
	dir := dedupeFixtureDir(t, "sprawl.go", dedupeSprawlSource)
	out, err := captureStdout(t, func() error { return runDedupeScan([]string{"--json", dir}) })
	mustErrContain(t, err, "1 utility sprawl finding")
	if !strings.Contains(out, `"sprawl_items"`) {
		t.Errorf("the failing report must still be printed as JSON:\n%s", out)
	}
	missing := "-dir-that-does-not-exist"
	if err := runDedupeScan([]string{"--", missing}); err == nil || !strings.Contains(err.Error(), missing) {
		t.Errorf(`a path after "--" must be read as the directory, got %v`, err)
	}
}
