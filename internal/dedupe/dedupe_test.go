package dedupe_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
	// The advice must name both audited entry points. util.RunGit alone inherits the
	// ambient environment and the inspected repository's configuration, which reproduced
	// the defect the rule exists to prevent; util.RunGitProbe alone is read-only by
	// construction, so a clone or a commit that follows it loses its hooks at a five-second
	// cap. .golangci.yml forbidigo names the same pair for the same call, and
	// TestScanRepo_Boundary_SprawlAdviceMatchesTheLinterRule binds the two files.
	replacement := report.SprawlItems[0].Replacement
	for _, want := range []string{"util.RunGit(", "util.RunGitProbe("} {
		if !strings.Contains(replacement, want) {
			t.Errorf("sprawl replacement = %q, want it to name %s", replacement, want)
		}
	}
	if report.Passed {
		t.Fatal("expected report to fail due to duplicates and sprawl")
	}
}

// TestScanRepo_Positive_CommandContextGitIsReported is the regression for the
// exec.CommandContext arm of the sprawl rule.
//
// Measured before the fix: the rule admitted CommandContext by name and then asserted the
// literal "git" at Args[0], which for CommandContext is the context expression and never a
// BasicLit, so the arm was dead by construction and the call was never reported.
func TestScanRepo_Positive_CommandContextGitIsReported(t *testing.T) {
	tmp := t.TempDir()
	source := `package test

import (
	"context"
	"os/exec"
)

func CallGit(ctx context.Context, tool string) {
	_ = exec.CommandContext(ctx, "git", "status")
	// Negative: a name that is not a literal "git", a non-git binary, and a call with no
	// name argument at all must stay unreported.
	_ = exec.CommandContext(ctx, tool, "status")
	_ = exec.CommandContext(ctx, "go", "build")
	_ = exec.Command(tool, "status")
	_ = exec.CommandContext(ctx)
}
`
	if err := os.WriteFile(filepath.Join(tmp, "ctx.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := dedupe.ScanRepo(tmp)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}
	if len(report.SprawlItems) != 1 {
		t.Fatalf("SprawlItems = %+v, want exactly the CommandContext git call", report.SprawlItems)
	}
	if pattern := report.SprawlItems[0].Pattern; !strings.Contains(pattern, "CommandContext") {
		t.Errorf("pattern = %q, want it to name the CommandContext form", pattern)
	}
	if report.Passed {
		t.Error("a repository with an unresolved sprawl finding must not pass")
	}
}

// TestScanRepoGitScope_Negative_HostileFsmonitorNeverRuns is the regression for the scan
// running under the scanned repository's control.
//
// Measured before the fix: a fixture repository whose local config sets core.fsmonitor to a
// shell script executed that script during ScanRepoContext and still reported Passed.
func TestScanRepoGitScope_Negative_HostileFsmonitorNeverRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fsmonitor fixture is a POSIX shell script")
	}
	dir, aux := t.TempDir(), t.TempDir()
	marker := filepath.Join(aux, "fsmonitor-ran")
	hook := filepath.Join(aux, "hostile.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\n: > "+marker+"\nexit 1\n"), 0o700); err != nil {
		t.Fatalf("write fsmonitor fixture: %v", err)
	}
	runGit(t, dir, "init")
	writeFile(t, dir, "tracked.go", duplicateBody)
	runGit(t, dir, "add", "tracked.go")
	runGit(t, dir, "config", "core.fsmonitor", hook)
	runGit(t, dir, "config", "core.hooksPath", filepath.Join(aux, "hooks"))

	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil {
		t.Fatalf("scan under a hostile configuration failed: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the scanned repository's fsmonitor command executed during the scan")
	}
	// Boundary: isolation must not cost the inventory. The tracked source is still there.
	if report.TotalFilesScanned != 1 {
		t.Errorf("TotalFilesScanned = %d, want the one tracked source", report.TotalFilesScanned)
	}
}

// TestScanRepo_Negative_FailsWithoutReport drives the exported ScanRepo wrapper, not only
// ScanRepoContext: a missing root or an unparsable source must yield an error and no
// report, never a clean score.
func TestScanRepo_Negative_FailsWithoutReport(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if report, err := dedupe.ScanRepo(missing); err == nil || report != nil {
		t.Fatalf("missing root must fail: %+v, %v", report, err)
	}
	broken := t.TempDir()
	writeFile(t, broken, "source.go", "package broken\nfunc (")
	if report, err := dedupe.ScanRepo(broken); err == nil || report != nil {
		t.Fatalf("unparsable source must fail: %+v, %v", report, err)
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
	runGit(t, tmp, "config", "user.email", "test@cordana.ai")
	runGit(t, tmp, "config", "user.name", "Praetor Test")

	// Commit 1
	writeFile(t, tmp, "README.md", "# Test\n")
	runGit(t, tmp, "add", "README.md")
	runGit(t, tmp, "commit", "-m", "init")

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
	if err == nil {
		t.Fatal("expected error for non-git dir")
	}
	if shouldRun || delta != 0 {
		t.Fatalf("expected shouldRun=false, delta=0, got %v, %d", shouldRun, delta)
	}
}

const duplicateBody = `package sample
func example(value int) int {
	value += 1
	value *= 2
	value -= 3
	return value
}
`

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if output, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestScanRepoGitScope(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	writeFile(t, dir, ".gitignore", ".claude/worktrees/\nignored/\n")
	writeFile(t, dir, "tracked.go", duplicateBody)
	writeFile(t, dir, ".claude/worktrees/clone/a.go", duplicateBody)
	writeFile(t, dir, "ignored/new.go", duplicateBody)
	runGit(t, dir, "add", ".gitignore", "tracked.go")
	report, err := dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil || !report.Passed || report.TotalFilesScanned != 1 {
		t.Fatalf("ignored clone must not enter report: %+v, %v", report, err)
	}

	// A tracked source remains in scope even if an ignore rule later covers it.
	runGit(t, dir, "add", "--force", "ignored/new.go")
	writeFile(t, dir, " leading source.go", duplicateBody)
	report, err = dedupe.ScanRepoContext(t.Context(), dir)
	if err != nil || report.TotalFilesScanned != 3 || len(report.Duplicates) != 1 {
		t.Fatalf("tracked ignored and nonignored untracked sources must be scanned: %+v, %v", report, err)
	}
	locations := report.Duplicates[0].Locations
	if len(locations) != 3 || locations[0].Path != " leading source.go" {
		t.Fatalf("NUL-delimited inventory must preserve whitespace paths: %+v", locations)
	}
}

func TestScanRepoRejectsIncompleteSources(t *testing.T) {
	for _, content := range []string{"package broken\nfunc (", "package valid\n" + string(make([]byte, (1<<20)+1))} {
		dir := t.TempDir()
		writeFile(t, dir, "source.go", content)
		if report, err := dedupe.ScanRepoContext(t.Context(), dir); err == nil || report != nil {
			t.Fatalf("incomplete source must not yield clean report: %+v, %v", report, err)
		}
	}
}

func TestScanRepoContextAndRootErrors(t *testing.T) {
	dir := t.TempDir()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := dedupe.ScanRepoContext(cancelled, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	var absent context.Context
	if report, err := dedupe.ScanRepoContext(absent, dir); err == nil || report != nil {
		t.Fatalf("nil context must fail: %+v, %v", report, err)
	}
	if report, err := dedupe.ScanRepoContext(t.Context(), filepath.Join(dir, "missing")); err == nil || report != nil {
		t.Fatalf("missing root must fail: %+v, %v", report, err)
	}
	writeFile(t, dir, ".git", "gitdir: missing-metadata\n")
	if report, err := dedupe.ScanRepoContext(t.Context(), dir); err == nil || report != nil {
		t.Fatalf("corrupt Git metadata must not trigger filesystem fallback: %+v, %v", report, err)
	}
}

func TestCadenceCorruptStateFails(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	writeFile(t, dir, ".workingdir/cadence.json", "{")
	if _, _, err := dedupe.CheckCadence(t.Context(), dir, 5); err == nil {
		t.Fatal("corrupt cadence must not be treated as absent")
	}
}

func TestCadenceRejectsLinkedStateDirectory(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	if err := os.Symlink(outside, filepath.Join(dir, ".workingdir")); err != nil {
		t.Fatal(err)
	}
	if err := dedupe.RecordCadence(t.Context(), dir, 5); err == nil {
		t.Fatal("cadence write followed a symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cadence write modified external directory: %v, %v", entries, err)
	}
}

// golangciConfig is the slice of .golangci.yml this package's advice has to agree with:
// linters.settings.forbidigo.forbid, where each entry carries the pattern it forbids, the
// package the symbol comes from, and the message golangci-lint prints instead.
type golangciConfig struct {
	Linters struct {
		Settings struct {
			Forbidigo struct {
				Forbid []struct {
					Pattern string `yaml:"pattern"`
					Pkg     string `yaml:"pkg"`
					Msg     string `yaml:"msg"`
				} `yaml:"forbid"`
			} `yaml:"forbidigo"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// auditedHelpers returns the util helper names a piece of advice recommends, read out of its
// "util.Name(" call forms. The names are derived from the advice rather than written down a
// third time, so an assertion built on them follows whatever the advice says.
func auditedHelpers(advice string) []string {
	parts := strings.Split(advice, "util.")
	names := make([]string, 0, len(parts))
	for _, part := range parts[1:] {
		if end := strings.Index(part, "("); end > 0 {
			names = append(names, part[:end])
		}
	}
	return names
}

// forbiddenExecMessage returns the forbidigo message .golangci.yml prints for a direct
// os/exec constructor, and reports whether the rule is still there at all.
func forbiddenExecMessage(t *testing.T) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".golangci.yml"))
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}
	var cfg golangciConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse .golangci.yml: %v", err)
	}
	for _, rule := range cfg.Linters.Settings.Forbidigo.Forbid {
		if strings.Contains(rule.Pkg, "os/exec") && strings.Contains(rule.Pattern, "Command") {
			return rule.Msg, true
		}
	}
	return "", false
}

// TestScanRepo_Boundary_SprawlAdviceMatchesTheLinterRule binds the two copies of one piece
// of advice so they cannot drift apart silently.
//
// The same bare git call is answered twice: internal/dedupe prints SprawlItem.Replacement
// when the sweep runs, and .golangci.yml forbidigo prints its own message when the linter
// runs. gitSprawlReplacement's comment asserted the two carry the same helper pair and
// nothing checked it, which is how the earlier contradiction (the linter naming one helper,
// the sweep naming another) got in. This reads the pair out of the scan's own advice and
// requires the linter message to name every helper in it: drop one from either file and this
// fails (HISS-20).
func TestScanRepo_Boundary_SprawlAdviceMatchesTheLinterRule(t *testing.T) {
	tmp := t.TempDir()
	source := `package test

import "os/exec"

func CallGit() {
	_ = exec.Command("git", "status")
}
`
	if err := os.WriteFile(filepath.Join(tmp, "sprawl.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := dedupe.ScanRepo(tmp)
	if err != nil {
		t.Fatalf("scan repo failed: %v", err)
	}
	if len(report.SprawlItems) != 1 {
		t.Fatalf("SprawlItems = %+v, want exactly the bare git call", report.SprawlItems)
	}

	helpers := auditedHelpers(report.SprawlItems[0].Replacement)
	// Boundary: fewer than two helpers means the advice stopped naming both audited entry
	// points, which is the defect this pair of assertions exists for -- not a reason to pass.
	if len(helpers) < 2 {
		t.Fatalf("sprawl advice %q names %v, want both audited entry points",
			report.SprawlItems[0].Replacement, helpers)
	}

	msg, ok := forbiddenExecMessage(t)
	if !ok {
		t.Fatal("no .golangci.yml forbidigo rule forbids the os/exec constructors any more")
	}
	for _, helper := range helpers {
		if !strings.Contains(msg, helper) {
			t.Errorf("the forbidigo message for exec.Command does not name util.%s: %q vs the sweep's %q",
				helper, msg, report.SprawlItems[0].Replacement)
		}
	}
}
