package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runTopologyAuditOn audits devRoot through the command and returns its output.
func runTopologyAuditOn(t *testing.T, devRoot string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return runTopologyAudit(context.Background(), []string{"--dev-root=" + devRoot})
	})
}

// TestTopologyAudit_ViolationWithoutStrayFileFails pins BUG-817: a repository directly in
// the dev root is a DEV-01 violation with no stray file, and it used to print [PASS].
func TestTopologyAudit_ViolationWithoutStrayFileFails(t *testing.T) {
	devRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(devRoot, "rogue", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runTopologyAuditOn(t, devRoot)
	if err == nil || !strings.Contains(err.Error(), "1 invariant violations") {
		t.Fatalf("err = %v, want an invariant-violation failure", err)
	}
	if !strings.Contains(out, "DEV-01: repository rogue") || strings.Contains(out, "[PASS]") {
		t.Fatalf("output must list the violation and claim no pass:\n%s", out)
	}
}

// TestTopologyAudit_CleanTreeNamesOnlyEvaluatedRules: the pass line names the rules the
// audit evaluates, not DEV-03 through DEV-05.
func TestTopologyAudit_CleanTreeNamesOnlyEvaluatedRules(t *testing.T) {
	devRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(devRoot, "cordanaLLM", "praetor", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := runTopologyAuditOn(t, devRoot)
	if err != nil {
		t.Fatalf("clean tree failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[PASS] Workstation topology complies with DEV-01 and DEV-02") {
		t.Fatalf("missing scoped pass line:\n%s", out)
	}
	for _, claim := range []string{"DEV-05", "100% compliant"} {
		if strings.Contains(out, claim) {
			t.Errorf("pass output still claims %q:\n%s", claim, out)
		}
	}
}

// TestTopologyAudit_ViolationAndStrayFileBothReported: both findings reach the error.
func TestTopologyAudit_ViolationAndStrayFileBothReported(t *testing.T) {
	devRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(devRoot, "rogue", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "CLAUDE.md"), []byte("stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runTopologyAuditOn(t, devRoot)
	if err == nil || !strings.Contains(err.Error(), "1 invariant violations and 1 stray governance files") {
		t.Fatalf("err = %v, want both counts", err)
	}
}
