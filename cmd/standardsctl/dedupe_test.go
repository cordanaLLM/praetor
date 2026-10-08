package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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
// and whose second commit adds twelve lines of Go production source. The commits run under
// testsupport.HermeticGitEnv: no workstation signing configuration applies, and no detached
// maintenance process is still writing below .git when t.TempDir cleans up.
func cadenceFixture(t *testing.T) string {
	t.Helper()
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit := []string{"commit", "-q", "-m"}
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

// #161: a polyglot repository with a small Go module used to get "Passed: true" with no word
// about the source the scan never read. The verdict now says partial and names the rest.
func TestRunDedupeScan_Positive_PolyglotVerdictSaysPartial(t *testing.T) {
	dir := dedupeFixtureDir(t, "tooling/clean.go", dedupeCleanSource)
	writeFixtureFile(t, dir, "crates/core/src/lib.rs", "pub fn add() {}\n")
	writeFixtureFile(t, dir, "crates/core/src/util.rs", "pub fn sub() {}\n")
	writeFixtureFile(t, dir, "crates/core/native/shim.c", "int shim(void) { return 0; }\n")

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err != nil {
		t.Fatalf("a clean Go module beside other languages still passes: %v\n%s", err, out)
	}
	mustContain(t, out, "Passed:            true (partial: Go sources only)",
		"Not Scanned:       c (1 file), rust (2 files)",
		"HISS-19 is not measured for these languages")
}

func TestRunDedupeScan_Negative_GoOnlyVerdictIsNotQualified(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	writeFixtureFile(t, dir, "README.md", "# docs are not source\n")

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err != nil {
		t.Fatalf("a clean Go-only repository must pass: %v", err)
	}
	if strings.Contains(out, "partial") || strings.Contains(out, "Not Scanned") {
		t.Fatalf("a Go-only repository must get an unqualified verdict:\n%s", out)
	}
}

// A repository with no Go at all still reports no verdict, now naming what it holds, and the
// JSON form carries the same coverage fields.
func TestRunDedupeScan_Boundary_NoGoNamesTheLanguagesAndJSONCarriesThem(t *testing.T) {
	dir := dedupeFixtureDir(t, "src/app.ts", "export const a = 1;\n")
	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err != nil {
		t.Fatalf("a repository without Go is not a failure: %v", err)
	}
	mustContain(t, out, "Not applicable: no Go sources found", "Not Scanned:       typescript (1 file)")
	if strings.Contains(out, "Passed:") {
		t.Fatalf("no verdict may be printed for a tree the detector never read:\n%s", out)
	}

	poly := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	writeFixtureFile(t, poly, "scripts/run.py", "print('x')\n")
	out, err = captureStdout(t, func() error { return runDedupeScan([]string{"--json", poly}) })
	if err != nil {
		t.Fatal(err)
	}
	var report dedupe.DedupeReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("JSON report: %v\n%s", err, out)
	}
	if !report.Partial || report.Unscanned["python"] != 1 {
		t.Fatalf("JSON report must carry partial coverage: %+v", report)
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

const deepCopyGeneratedClonesFixture = `// Code generated by controller-gen. DO NOT EDIT.
package sample

type Alpha struct {
	Name  string
	Tags  []string
	Count *int
}

func (in *Alpha) DeepCopyInto(out *Alpha) {
	*out = *in
	if in.Tags != nil {
		in, out := &in.Tags, &out.Tags
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Count != nil {
		in, out := &in.Count, &out.Count
		*out = new(int)
		**out = **in
	}
}

type Beta struct {
	Name  string
	Tags  []string
	Count *int
}

func (in *Beta) DeepCopyInto(out *Beta) {
	*out = *in
	if in.Tags != nil {
		in, out := &in.Tags, &out.Tags
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.Count != nil {
		in, out := &in.Count, &out.Count
		*out = new(int)
		**out = **in
	}
}
`

func TestRunDedupeScan_Positive_ExceptedDeepCopyClonesPassAndAreListedAsExcepted(t *testing.T) {
	dir := dedupeFixtureDir(t, "zz_generated.deepcopy.go", deepCopyGeneratedClonesFixture)
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	manifest := fmt.Sprintf(`exceptions:
  - rule: HISS-19
    path: zz_generated.deepcopy.go
    reason: generated-style DeepCopyInto clones
    expires: %s
`, expires)
	writeFixtureFile(t, dir, config.ManifestFileName, manifest)

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err != nil {
		t.Fatalf("scan with excepted clones must pass: %v", err)
	}
	mustContain(t, out,
		"Passed:            true",
		"Excepted Duplicate Function Blocks (1):",
		"zz_generated.deepcopy.go",
		fmt.Sprintf("excepted until %s by the exceptions entry (rule HISS-19, zz_generated.deepcopy.go): generated-style DeepCopyInto clones", expires),
	)
	if strings.Contains(out, "\nDuplicate Function Blocks") {
		t.Errorf("excepted clones must not be listed as unexcused duplicates:\n%s", out)
	}
}

func TestRunDedupeScan_Negative_DeepCopyClonesWithoutEntryFail(t *testing.T) {
	dir := dedupeFixtureDir(t, "zz_generated.deepcopy.go", deepCopyGeneratedClonesFixture)

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err == nil {
		t.Fatal("clones without exceptions entry must fail scan")
	}
	mustContain(t, out,
		"Duplicate Function Blocks (1):",
		"zz_generated.deepcopy.go",
	)
}

func TestRunDedupeScan_Negative_ExpiredEntryFailsNamingIt(t *testing.T) {
	dir := dedupeFixtureDir(t, "zz_generated.deepcopy.go", deepCopyGeneratedClonesFixture)
	manifest := `exceptions:
  - rule: HISS-19
    path: zz_generated.deepcopy.go
    reason: generated-style DeepCopyInto clones
    expires: 2020-01-01
`
	writeFixtureFile(t, dir, config.ManifestFileName, manifest)

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err == nil {
		t.Fatal("expired exceptions entry must fail scan")
	}
	mustContain(t, out,
		"Duplicate Function Blocks (1):",
		"zz_generated.deepcopy.go",
		"(its HISS-19 exception expired on 2020-01-01)",
	)
}

func TestRunDedupeScan_Negative_MissingExceptionTargetRefusedByValidation(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	manifest := fmt.Sprintf(`exceptions:
  - rule: HISS-19
    path: nonexistent.go
    reason: missing file
    expires: %s
`, expires)
	writeFixtureFile(t, dir, config.ManifestFileName, manifest)

	err := runDedupeScan([]string{dir})
	if err == nil {
		t.Fatal("missing file exception target must be refused by validation")
	}
	mustErrContain(t, err, "exceptions[0] target file does not exist: nonexistent.go")
}

func TestRunDedupeScan_Negative_StaleExceptionFailsNamingIt(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	manifest := fmt.Sprintf(`exceptions:
  - rule: HISS-19
    path: clean.go
    reason: clean file has no clones
    expires: %s
`, expires)
	writeFixtureFile(t, dir, config.ManifestFileName, manifest)

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err == nil {
		t.Fatal("stale exceptions entry must fail scan")
	}
	mustContain(t, out,
		"Passed:            false",
		"Stale Exceptions (1):",
		"- exceptions entry clean.go (HISS-19): excuses no duplicate function block; remove the entry",
	)
	mustErrContain(t, err, "1 stale exception(s)")
}

func TestRunDedupeScan_Negative_StaleExceptionForNonGoFileFails(t *testing.T) {
	dir := dedupeFixtureDir(t, "clean.go", dedupeCleanSource)
	writeFixtureFile(t, dir, "README.md", "# Demo\n")
	expires := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	manifest := fmt.Sprintf(`exceptions:
  - rule: HISS-19
    path: README.md
    reason: non-Go file
    expires: %s
`, expires)
	writeFixtureFile(t, dir, config.ManifestFileName, manifest)

	out, err := captureStdout(t, func() error { return runDedupeScan([]string{dir}) })
	if err == nil {
		t.Fatal("stale exception for non-Go file must fail scan")
	}
	mustContain(t, out,
		"Passed:            false",
		"Stale Exceptions (1):",
		"- exceptions entry README.md (HISS-19): excuses no duplicate function block; remove the entry",
	)
	mustErrContain(t, err, "1 stale exception(s)")
}
