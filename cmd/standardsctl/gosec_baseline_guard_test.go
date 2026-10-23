// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func gosecRecordFixture(t *testing.T) *auditFixture {
	t.Helper()
	t.Setenv("PATH", testsupport.PinnedGosecDir(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "go.mod", "module example.com/widget\n\ngo 1.22\n")
	writeFixtureFile(t, f.dir, gating.GosecConfigFile, "{\"global\":{\"exclude\":\"\"}}\n")
	writeFixtureFile(t, f.dir, "vuln.go", fixtureGosecVuln)
	return f
}

// seedHistoricalGosec models debt already committed on the default branch.
func seedHistoricalGosec(t *testing.T, f *auditFixture) {
	t.Helper()
	findings, _, err := gating.ScanGosec(t.Context(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := baseline.Record(nil, findings, baseline.RecordOptions{AllowIncrease: true, Rationale: "historical fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.SaveBaseline(filepath.Join(f.dir, gating.GosecBaselineFile), b); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, f.dir, f.gitEnv, "historical gosec baseline")
}

func TestGosecFirstBaselineGuard_Planted(t *testing.T) {
	f := gosecRecordFixture(t)
	_, err := runBaselineCmd(t, f, "--record", "--allow-gosec-increase", "--gosec-reason=initial debt")
	if err == nil {
		t.Fatal("first gosec baseline accepted with planted G204")
	}
	mustContain(t, err.Error(), "no first gosec baseline", "#nosec Gxxx -- reason", ".standards.yaml", "fix")
	if _, err := os.Stat(filepath.Join(f.dir, gating.GosecBaselineFile)); !os.IsNotExist(err) {
		t.Fatalf("refusal wrote gosec baseline: %v", err)
	}
}

func TestGosecFirstBaselineGuard_WorkingTreeCannotAuthorize(t *testing.T) {
	f := gosecRecordFixture(t)
	seedHistoricalGosec(t, f)
	// The remote default branch has no baseline, despite the local branch carrying one.
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "update-ref", "refs/remotes/origin/main", "HEAD~1"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_, err := runBaselineCmd(t, f, "--record")
	if err == nil {
		t.Fatal("working baseline bypassed absent default-branch baseline")
	}
	mustErrContain(t, err, "no first gosec baseline")
}

func TestGosecFirstBaselineGuard_GrowthFlagRefused(t *testing.T) {
	f := gosecRecordFixture(t)
	seedHistoricalGosec(t, f)
	_, err := runBaselineCmd(t, f, "--record", "--allow-gosec-increase", "--gosec-reason=more debt")
	if err == nil {
		t.Fatal("--allow-gosec-increase accepted")
	}
	mustErrContain(t, err, "no first gosec baseline")
}

func TestGosecFirstBaselineGuard_CleanLeavesNoFile(t *testing.T) {
	f := gosecRecordFixture(t)
	writeFixtureFile(t, f.dir, "vuln.go", "package widgets\n")
	if _, err := runBaselineCmd(t, f, "--record"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, gating.GosecBaselineFile)); !os.IsNotExist(err) {
		t.Fatalf("clean first record created baseline: %v", err)
	}
}

func TestGosecFirstBaselineGuard_ShrinkAccepted(t *testing.T) {
	f := gosecRecordFixture(t)
	seedHistoricalGosec(t, f)
	writeFixtureFile(t, f.dir, "vuln.go", "package widgets\n")
	if _, err := runBaselineCmd(t, f, "--record"); err != nil {
		t.Fatalf("shrinking historical gosec baseline refused: %v", err)
	}
	b, err := baseline.LoadBaseline(filepath.Join(f.dir, gating.GosecBaselineFile))
	if err != nil {
		t.Fatal(err)
	}
	if b.Absent || b.TotalInfractions != 0 {
		t.Fatalf("expected historical baseline shrunk to zero, got %+v", b)
	}
}

func TestGosecFirstBaselineGuard_DefaultBranchGrowthRefused(t *testing.T) {
	f := gosecRecordFixture(t)
	seedHistoricalGosec(t, f)
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "update-ref", "refs/remotes/origin/main", "HEAD"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	writeFixtureFile(t, f.dir, "extra.go", strings.Replace(fixtureGosecVuln, "runVuln", "runExtra", 1))
	// Even a hand-raised working baseline cannot authorize growth above origin/main.
	findings, _, err := gating.ScanGosec(t.Context(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	inflated, err := baseline.Record(nil, findings, baseline.RecordOptions{AllowIncrease: true, Rationale: "inflated fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.SaveBaseline(filepath.Join(f.dir, gating.GosecBaselineFile), inflated); err != nil {
		t.Fatal(err)
	}
	_, err = runBaselineCmd(t, f, "--record")
	if err == nil {
		t.Fatal("growth beyond default branch accepted")
	}
	mustErrContain(t, err, "no first gosec baseline")
}
