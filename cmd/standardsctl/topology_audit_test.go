package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/topology"
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

func writeTopologyFiller(t *testing.T, dir string, count int) {
	t.Helper()
	for i := range count {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("filler-%04d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestTopologyAudit_TruncatedScanFails pins BUG-904: a scan cut at MaxScanEntries lists
// the truncation and never prints the pass line.
func TestTopologyAudit_TruncatedScanFails(t *testing.T) {
	devRoot := t.TempDir()
	writeTopologyFiller(t, devRoot, topology.MaxScanEntries+1)
	out, err := runTopologyAuditOn(t, devRoot)
	if err == nil || !strings.Contains(err.Error(), "incomplete scan (1 truncation reasons)") {
		t.Fatalf("err = %v, want an incomplete-scan failure", err)
	}
	if !strings.Contains(out, "Incomplete Scan (1)") || !strings.Contains(out, "MaxScanEntries") {
		t.Fatalf("output does not list the truncation:\n%s", out)
	}
	if strings.Contains(out, "[PASS]") {
		t.Fatalf("truncated scan printed a pass line:\n%s", out)
	}
}

// TestTopologyAudit_ScanAtBoundPasses: exactly MaxScanEntries entries is a complete scan.
func TestTopologyAudit_ScanAtBoundPasses(t *testing.T) {
	devRoot := t.TempDir()
	writeTopologyFiller(t, devRoot, topology.MaxScanEntries)
	out, err := runTopologyAuditOn(t, devRoot)
	if err != nil || strings.Contains(out, "Incomplete Scan") {
		t.Fatalf("complete scan at the bound failed: %v\n%s", err, out)
	}
}

func TestTopologyAuditVerdict_TruncationJoinsOtherProblems(t *testing.T) {
	report := &topology.TopologyReport{
		Truncated:         true,
		TruncationReasons: []string{"organization container lusoris could not be read"},
		Violations:        []string{"DEV-01: repository rogue"},
	}
	err := topologyAuditVerdict(report)
	want := "topology audit failed with an incomplete scan (1 truncation reasons) and 1 invariant violations"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// TestTopologyClean_RefusesTruncatedAudit: cleanup deletes nothing when the audit behind it
// did not cover the whole tree.
func TestTopologyClean_RefusesTruncatedAudit(t *testing.T) {
	devRoot := t.TempDir()
	stray := filepath.Join(devRoot, "CLAUDE.md")
	if err := os.WriteFile(stray, []byte("stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTopologyFiller(t, devRoot, topology.MaxScanEntries)
	out, err := captureStdout(t, func() error {
		return runTopologyClean(context.Background(), []string{"--dev-root=" + devRoot, "--dry-run=false"})
	})
	if !errors.Is(err, topology.ErrScanTruncated) {
		t.Fatalf("err = %v, want ErrScanTruncated", err)
	}
	assertNoTopologySuccessClaim(t, out)
	if _, statErr := os.Stat(stray); statErr != nil {
		t.Fatalf("truncated clean deleted a finding: %v", statErr)
	}
}
