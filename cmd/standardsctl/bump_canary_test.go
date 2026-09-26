package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/bump"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestBumpCanaryCLIDryRunIsOnlyPlanned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	text, err := captureStdout(t, func() error {
		return runBumpCanary(t.Context(), []string{"example.org/dependency", "--target=v1.2.3", "--dry-run", "--path", dir})
	})
	if err != nil || !strings.Contains(text, "[PLANNED]") || !strings.Contains(text, "Canary Certified: false") || strings.Contains(text, "[PASS]") {
		t.Fatalf("dry-run misrepresented: %q, %v", text, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created files: %v", err)
	}
}

func TestBumpApplyCLIRejectsMissingPatchBeforeUpdate(t *testing.T) {
	dir := t.TempDir()
	text, err := captureStdout(t, func() error {
		return runBumpApply(t.Context(), []string{"example.org/dependency", "--version=v1.2.3", "--path", dir, "--patch", filepath.Join(dir, "missing.patch")})
	})
	if !errors.Is(err, os.ErrNotExist) || !errors.Is(err, bump.ErrInvalidPatch) || strings.Contains(text, "[APPLIED]") || strings.Contains(text, "[PATCHED]") {
		t.Fatalf("missing patch skipped or claimed applied: %q, %v", text, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing patch caused side effects: %v, %v", entries, err)
	}
}

func TestBumpTrainCLIDryRunDoesNotClaimPatchOrCertification(t *testing.T) {
	useOutdatedFixtureScanner(t)
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", `{"dependencies":{"fixture-dep":"2.0.0-rc.1"}}`)
	text, err := captureStdout(t, func() error {
		return runBumpTrain(t.Context(), []string{"--dry-run", "--path", dir})
	})
	if err != nil || !strings.Contains(text, "[planned]") || strings.Contains(text, "[CERTIFIED]") || strings.Contains(text, "patch staged") {
		t.Fatalf("train fabricated evidence: %q, %v", text, err)
	}
}

func TestBumpTrainCLIPropagatesFailedCanary(t *testing.T) {
	useOutdatedFixtureScanner(t)
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", `{"dependencies":{"fixture-dep":"2.0.0-rc.1"}}`)
	writeFixtureFile(t, dir, ".gitignore", ".standards/\n.workingdir/\n")
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Canary Fixture"}, {"config", "user.email", "fixture@example.test"}, {"add", "."}, {"commit", "-q", "-s", "-m", "test: initialize canary fixture"}} {
		if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	text, err := captureStdout(t, func() error {
		return runBumpTrain(t.Context(), []string{"--path", dir})
	})
	if !errors.Is(err, bump.ErrCanaryFailed) || !strings.Contains(text, "[ERROR]") || strings.Contains(text, "[CERTIFIED]") {
		t.Fatalf("failed train did not propagate failure: %q, %v", text, err)
	}
}

// A train entry stopped by the shared deadline is labelled CANCELLED; a real
// failure, and an error that merely mentions cancellation, stay ERROR.
func TestCanaryErrorLabelSeparatesCancellationFromFailure(t *testing.T) {
	for err, want := range map[error]string{
		fmt.Errorf("%w: test command: %w", bump.ErrCanaryCancelled, context.DeadlineExceeded): "CANCELLED",
		fmt.Errorf("%w: test command: exit status 1", bump.ErrCanaryFailed):                   "ERROR",
		errors.New("canary cancelled"): "ERROR",
	} {
		if got := canaryErrorLabel(err); got != want {
			t.Errorf("canaryErrorLabel(%v) = %s, want %s", err, got, want)
		}
	}
}

// outdatedFixturePnpm stands in for a pnpm that reports fixture-dep outdated and fails
// every other command, so the train has exactly one real candidate whose canary test fails.
const outdatedFixturePnpm = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "outdated" {
		fmt.Print("{\"fixture-dep\":{\"current\":\"2.0.0-rc.1\",\"latest\":\"2.0.0-rc.2\"}}")
	}
	os.Exit(1)
}
`

// useOutdatedFixtureScanner puts outdatedFixturePnpm first on PATH. A scan whose pnpm
// reports nothing yields no candidates: the train only runs on a reported upgrade.
func useOutdatedFixtureScanner(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	testsupport.BuildExecutable(t, dir, "pnpm", outdatedFixturePnpm)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
