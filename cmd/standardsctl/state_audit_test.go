package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/state"
)

func TestStateAudit_Positive_DeclaredVerifyStageWithoutLedgerPasses(t *testing.T) {
	t.Setenv(gating.DeclaredVerifyGuardEnv, "1")
	dir := t.TempDir()

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	})
	if err != nil {
		t.Fatalf("state audit failed in declared-verify stage without ledger: %v", err)
	}
	want := stateAuditNotApplicableReason + "\n"
	if out != want {
		t.Fatalf("state audit output mismatch:\ngot:  %q\nwant: %q", out, want)
	}
}

func TestStateAudit_Negative_NoStageEnvironmentWithoutLedgerFails(t *testing.T) {
	if prev, existed := os.LookupEnv(gating.DeclaredVerifyGuardEnv); existed {
		if err := os.Unsetenv(gating.DeclaredVerifyGuardEnv); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Setenv(gating.DeclaredVerifyGuardEnv, prev); err != nil {
				t.Error(err)
			}
		})
	}
	dir := t.TempDir()

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	})
	if err == nil {
		t.Fatal("expected state audit to fail when no stage environment and no ledger, but it passed")
	}
	if !strings.Contains(out, "WorkingDir Exists: false") {
		t.Fatalf("expected output to contain 'WorkingDir Exists: false', got: %s", out)
	}
	if !strings.Contains(err.Error(), "state audit failed with 1 violations") {
		t.Fatalf("expected error to mention violations, got: %v", err)
	}
}

func TestStateAudit_Boundary_DeclaredVerifyStageWithInvalidLedgerFails(t *testing.T) {
	t.Setenv(gating.DeclaredVerifyGuardEnv, "1")
	dir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, state.WorkingDirName, "BACKLOG.md")); err != nil {
		t.Fatal(err)
	}

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	})
	if err == nil {
		t.Fatal("expected state audit to fail on invalid ledger even in declared-verify stage, but it passed")
	}
	if !strings.Contains(out, "WorkingDir Exists: true") {
		t.Fatalf("expected output to report WorkingDir Exists: true, got: %s", out)
	}
	if !strings.Contains(out, "BACKLOG.md") {
		t.Fatalf("expected output to report missing BACKLOG.md, got: %s", out)
	}
	if !strings.Contains(err.Error(), "state audit failed") {
		t.Fatalf("expected error to report state audit failure, got: %v", err)
	}
}

func TestStateAudit_Boundary_EmptyStageEnvironmentWithoutLedgerFails(t *testing.T) {
	t.Setenv(gating.DeclaredVerifyGuardEnv, "")
	dir := t.TempDir()

	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	})
	if err == nil {
		t.Fatal("expected state audit to fail when stage environment is empty and no ledger, but it passed")
	}
	if !strings.Contains(out, "WorkingDir Exists: false") {
		t.Fatalf("expected output to contain 'WorkingDir Exists: false', got: %s", out)
	}
	if !strings.Contains(err.Error(), "state audit failed with 1 violations") {
		t.Fatalf("expected error to mention violations, got: %v", err)
	}
}
