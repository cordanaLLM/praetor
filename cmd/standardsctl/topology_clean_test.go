package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/topology"
)

func TestRunTopologyCleanReportsBlockedEntries(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		t.Run(strconv.FormatBool(dryRun), func(t *testing.T) {
			assertBlockedTopologyClean(t, dryRun)
		})
	}
}

func assertBlockedTopologyClean(t *testing.T, dryRun bool) {
	t.Helper()
	devRoot := t.TempDir()
	gitPath := writeIndeterminateGovernanceRepo(t, devRoot)
	out, err := captureStdout(t, func() error {
		return runTopologyClean(context.Background(), []string{
			"--dev-root=" + devRoot,
			"--dry-run=" + strconv.FormatBool(dryRun),
		})
	})
	if err == nil {
		t.Fatal("blocked cleanup reported success")
	}
	if !strings.Contains(out, "MANUAL REVIEW REQUIRED") {
		t.Fatalf("blocked finding missing from output:\n%s", out)
	}
	assertNoTopologySuccessClaim(t, out)
	if _, statErr := os.Lstat(gitPath); statErr != nil {
		t.Fatalf("blocked metadata was not preserved: %v", statErr)
	}
}

func assertNoTopologySuccessClaim(t *testing.T, output string) {
	t.Helper()
	for _, falseClaim := range []string{"No stray governance", "[PASS]", "Topology restored"} {
		if strings.Contains(output, falseClaim) {
			t.Errorf("output contains false success claim %q:\n%s", falseClaim, output)
		}
	}
}

func TestRunTopologyCleanMixedResultIsIncomplete(t *testing.T) {
	devRoot := t.TempDir()
	safePath := filepath.Join(devRoot, "CLAUDE.md")
	if err := os.WriteFile(safePath, []byte("stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitPath := writeIndeterminateGovernanceRepo(t, devRoot)

	out, err := captureStdout(t, func() error {
		return runTopologyClean(context.Background(), []string{
			"--dev-root=" + devRoot,
			"--dry-run=false",
		})
	})
	if err == nil {
		t.Fatal("partial cleanup reported success")
	}
	if _, statErr := os.Stat(safePath); !os.IsNotExist(statErr) {
		t.Fatalf("safe stray file was not removed: %v", statErr)
	}
	if _, statErr := os.Lstat(gitPath); statErr != nil {
		t.Fatalf("blocked metadata was not preserved: %v", statErr)
	}
	if !strings.Contains(out, "Removed 1 safe stray") || !strings.Contains(out, "MANUAL REVIEW REQUIRED") {
		t.Fatalf("mixed result was not reported accurately:\n%s", out)
	}
	if strings.Contains(out, "[PASS]") || strings.Contains(out, "Topology restored") {
		t.Fatalf("partial cleanup contains a false success claim:\n%s", out)
	}
}

func TestRunTopologyCleanSafeOnlyReportsSuccess(t *testing.T) {
	devRoot := t.TempDir()
	safePath := filepath.Join(devRoot, "CLAUDE.md")
	if err := os.WriteFile(safePath, []byte("stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		return runTopologyClean(context.Background(), []string{
			"--dev-root=" + devRoot,
			"--dry-run=false",
		})
	})
	if err != nil {
		t.Fatalf("safe-only cleanup failed: %v", err)
	}
	if _, statErr := os.Stat(safePath); !os.IsNotExist(statErr) {
		t.Fatalf("safe stray file was not removed: %v", statErr)
	}
	if !strings.Contains(out, "[PASS] Successfully cleaned 1 safe stray files.") {
		t.Fatalf("safe cleanup success missing:\n%s", out)
	}
	if strings.Contains(out, "Topology restored") {
		t.Fatalf("output makes an unverified restoration claim:\n%s", out)
	}
}

func TestPrintTopologyCleanResultReportsPartialMutationWithoutSuccess(t *testing.T) {
	result := &topology.CleanResult{Cleaned: []string{"/tmp/already-removed"}}
	out, err := captureStdout(t, func() error {
		return printTopologyCleanResult(result, false, false)
	})
	if err != nil {
		t.Fatalf("print partial cleanup result: %v", err)
	}
	if !strings.Contains(out, "Removed 1 safe stray") {
		t.Fatalf("partial mutation missing from output:\n%s", out)
	}
	if strings.Contains(out, "[PASS]") || strings.Contains(out, "No stray") {
		t.Fatalf("partial mutation contains a false success claim:\n%s", out)
	}
}

func writeIndeterminateGovernanceRepo(t *testing.T, devRoot string) string {
	t.Helper()
	repo := filepath.Join(devRoot, "golusoris", "docs")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.Join(repo, ".git")
	if err := os.WriteFile(gitPath, []byte("not a gitlink\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return gitPath
}
