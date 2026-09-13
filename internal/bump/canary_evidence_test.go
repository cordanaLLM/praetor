package bump

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestDryRunDoesNotCertifyOrCreateState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent-repository")
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: root, DryRun: true,
		Candidate: UpgradeCandidate{Package: "example.org/dependency", TargetVersion: "v1.2.3", ManifestType: "go.mod"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.CanaryCertified || result.WorktreePath != "" || result.StagedPatchPath != "" {
		t.Fatalf("dry-run fabricated execution evidence: %+v", result)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("dry-run created repository state: %v", err)
	}
}

func TestCanaryOutputBoundaryCannotCertifyTruncation(t *testing.T) {
	for _, size := range []int{65536, 65537} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			dir, candidate := canaryFixture(t)
			executable := filepath.Join(t.TempDir(), "test-command")
			if err := os.WriteFile(executable, []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%%ds' x\n", size)), 0700); err != nil {
				t.Fatal(err)
			}
			result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: executable})
			if size == 65536 {
				if err != nil || !result.Success || len(result.ExecutionLog) != size {
					t.Fatalf("exact output boundary rejected: %+v, %v", result, err)
				}
			} else if !errors.Is(err, ErrCanaryFailed) || result.Success || result.Status != CanaryFailed {
				t.Fatalf("overflow misreported as success: %+v, %v", result, err)
			}
			if result.CanaryCertified {
				t.Fatal("command output incorrectly certified")
			}
		})
	}
}

func TestCanaryPropagatesManifestUpdateFailure(t *testing.T) {
	dir, candidate := canaryFixture(t)
	candidate.ManifestType = "unsupported"
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate})
	if !errors.Is(err, ErrCanaryFailed) || result.Success || result.CanaryCertified || result.Status != CanaryFailed {
		t.Fatalf("update failure hidden: %+v, %v", result, err)
	}
}

func canaryFixture(t *testing.T) (string, UpgradeCandidate) {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Canary Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"package.json": `{"dependencies":{"fixture-dep":"^1.0.0"}}` + "\n",
		"README.md":    "before\n", ".gitignore": ".standards/\n.workingdir/\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := util.RunGit(t.Context(), dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), dir, "commit", "-q", "-s", "-m", "test: initialize canary fixture"); err != nil {
		t.Fatal(err)
	}
	return dir, UpgradeCandidate{Package: "fixture-dep", CurrentVersion: "1.0.0", TargetVersion: "2.0.0", ManifestType: "package.json"}
}

func TestCanaryCommandPassingIsNotCertification(t *testing.T) {
	dir, candidate := canaryFixture(t)
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: "git status --porcelain"})
	if err != nil || !result.Success || result.Status != CanaryPassed || result.CanaryCertified {
		t.Fatalf("configured command result conflated with certification: %+v, %v", result, err)
	}
	if !strings.Contains(result.ExecutionLog, "package.json") {
		t.Fatalf("test did not observe updated worktree: %+v", result)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("temporary worktree not cleaned: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || !strings.Contains(string(body), "1.0.0") {
		t.Fatalf("canary changed source manifest: %s, %v", body, err)
	}
}

func TestCanaryFailureRetainsDiagnosticsNeverPatch(t *testing.T) {
	dir, candidate := canaryFixture(t)
	result, err := RunCanary(t.Context(), CanaryOptions{RepoPath: dir, Candidate: candidate, TestCmd: "git -c color.ui=always diff --exit-code"})
	if !errors.Is(err, ErrCanaryFailed) || result.Success || result.CanaryCertified || result.Status != CanaryFailed {
		t.Fatalf("failed test misreported: %+v, %v", result, err)
	}
	if result.StagedPatchPath != "" || filepath.Ext(result.DiagnosticPath) != ".sarif" {
		t.Fatalf("failure diagnostics missing or disguised as patch: %+v, %v", result, err)
	}
	if _, err := os.Stat(result.DiagnosticPath); err != nil {
		t.Fatalf("claimed diagnostic file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".standards", "patches")); !os.IsNotExist(err) {
		t.Fatalf("failure fabricated patch directory: %v", err)
	}
}

func TestCanaryCancellationNeverCreatesWorktree(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := RunCanary(ctx, CanaryOptions{RepoPath: dir})
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %+v, %v", result, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled canary wrote files: %v, %v", entries, err)
	}
}
