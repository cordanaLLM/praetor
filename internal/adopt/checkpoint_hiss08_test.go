// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

func renderCheckpointRepo(t *testing.T, sourceRoot string) string {
	t.Helper()
	repoPath := t.TempDir()
	session := &adoptSession{
		repoPath: repoPath,
		repoName: "test-checkpoint",
		identity: repoIdentity{owner: "acme", name: "test-checkpoint"},
		opts:     AdoptOptions{LockSourceRoot: sourceRoot},
		report:   &AdoptReport{},
	}
	ready, err := reconcileCheckpointBundle(t.Context(), session, false)
	if err != nil || !ready {
		t.Fatalf("reconcileCheckpointBundle: ready=%v err=%v", ready, err)
	}
	return repoPath
}

func assertKillpgViolation(t *testing.T, violations []hiss.InvariantViolation, wantLine int) {
	t.Helper()
	for _, v := range violations {
		if v.RuleID == "HISS-08" && v.LineNumber == wantLine {
			if strings.Contains(v.Message, "os.killpg") {
				return
			}
		}
	}
	t.Fatalf("missing HISS-08 violation on line %d: %+v", wantLine, violations)
}

// TestCheckpointBundle_HISS08_Positive proves rendering the checkpoint bundle from the
// repository's verified source root produces a tree with zero HISS-08 violations.
func TestCheckpointBundle_HISS08_Positive(t *testing.T) {
	sourceRoot := filepath.Join("..", "..")
	rendered := renderCheckpointRepo(t, sourceRoot)
	report, err := hiss.Scan(t.Context(), rendered, hiss.ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Incomplete() {
		t.Fatalf("scan incomplete: %+v", report.Skips)
	}
	if got := report.Breakdown["HISS-08"]; got != 0 {
		t.Fatalf("got %d HISS-08 findings, want 0; violations: %+v", got, report.Violations)
	}
}

// TestCheckpointBundle_HISS08_Negative proves overwriting common.py in the rendered bundle
// with the unguarded adopter-lint.common.py triggers a HISS-08 violation on line 117.
func TestCheckpointBundle_HISS08_Negative(t *testing.T) {
	sourceRoot := filepath.Join("..", "..")
	rendered := renderCheckpointRepo(t, sourceRoot)
	adopterLint := filepath.Join("testdata", "checkpoint", "common", "adopter-lint.common.py")
	content, err := os.ReadFile(adopterLint)
	if err != nil {
		t.Fatalf("read %s: %v", adopterLint, err)
	}
	target := filepath.Join(rendered, filepath.FromSlash(checkpointCommon))
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", target, err)
	}
	report, err := hiss.Scan(t.Context(), rendered, hiss.ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := report.Breakdown["HISS-08"]; got != 1 {
		t.Fatalf("got %d HISS-08 findings, want 1; violations: %+v", got, report.Violations)
	}
	assertKillpgViolation(t, report.Violations, 117)
}

// TestCheckpointBundle_HISS08_Boundary proves the repository's own common.py scanned directly
// passes HISS-08 without violations.
func TestCheckpointBundle_HISS08_Boundary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(checkpointCommon)))
	if err != nil {
		t.Fatalf("read %s: %v", checkpointCommon, err)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, filepath.FromSlash(checkpointCommon)), string(data))
	report, err := hiss.Scan(t.Context(), root, hiss.ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if report.Incomplete() {
		t.Fatalf("scan incomplete: %+v", report.Skips)
	}
	if got := report.Breakdown["HISS-08"]; got != 0 {
		t.Fatalf("got %d HISS-08 findings, want 0; violations: %+v", got, report.Violations)
	}
}
