// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

func TestReview8AbsentReadmeVerify(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\nadoption:\n  decline: [readme]\n")
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	out, err := runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, ".readme-baseline.json: not recorded")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, separatedReadmeFile) && strings.Contains(line, "[PASS]") {
			t.Errorf("unscanned README passed: %s", line)
		}
	}
	audit, err := f.audit(t, "--offline")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, audit, "[REPORT] README presentation measured, not enforced: 1 finding(s)")
}

func TestReview8AbsentReadmeReadoption(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\nadoption:\n  decline: [dev-container, readme]\n")
	rep, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, LockSourceRoot: f.dir, RecordBaseline: true, SkipHookActivation: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rep.BaselineRatchets {
		if v.File == separatedReadmeFile {
			t.Errorf("unrecorded README ratchet: %+v", v)
		}
	}
	for _, file := range rep.ReconciledFiles {
		if file == separatedReadmeFile {
			t.Errorf("nonexistent README file reconciled: %s", file)
		}
	}
	out, err := captureStdout(t, func() error { printDebtSummary(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, ".readme-baseline.json: not recorded")
}

func TestReview8RecordRefusalLeavesHISSUnchanged(t *testing.T) {
	f := newAuditFixture(t)
	f.writeBaseline(t, []baseline.Infraction{{RuleID: "HISS-07", FilePath: "removed.go", Anchor: "fn:removed"}}, "")
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\ngate:\n  readme_presentation: true\n")
	writeFixtureFile(t, f.dir, separatedReadmeFile, `{"version":1,"infractions":[]}`)
	before := readFixtureFile(t, f.dir, cliBaseline)
	_, err := runBaselineCmd(t, f, "--record")
	if err == nil {
		t.Fatal("README increase was accepted")
	}
	if after := readFixtureFile(t, f.dir, cliBaseline); after != before {
		t.Errorf("README refusal rewrote HISS baseline")
	}
	mustErrContain(t, err, "--allow-increase --reason")
}

func TestReview8MixedBaseSnapshot(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\nadoption:\n  decline: [readme]\n")
	findings, err := readmepresentation.CheckFile(t.Context(), f.dir, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	entries := append([]baseline.Infraction{f.addViolation(t)}, readmepresentation.ToBaseline(findings)...)
	f.writeBaseline(t, entries, "")
	gitCommitAll(t, f.dir, f.gitEnv, "legacy mixed snapshot")
	if _, err := baseline.MigrateReadme(t.Context(), f.baselinePath, filepath.Join(f.dir, separatedReadmeFile), false); err != nil {
		t.Fatal(err)
	}
	hiss, readme := readFixtureFile(t, f.dir, cliBaseline), readFixtureFile(t, f.dir, separatedReadmeFile)
	out, err := f.audit(t, "--offline", "--base=HEAD")
	if err != nil {
		t.Fatalf("mixed base snapshot refused: %v", err)
	}
	mustContain(t, out, ".standards-baseline.json does not grow versus HEAD (1 -> 1)", ".readme-baseline.json does not grow versus HEAD (1 -> 1)")
	if hiss != readFixtureFile(t, f.dir, cliBaseline) || readme != readFixtureFile(t, f.dir, separatedReadmeFile) {
		t.Fatal("audit changed migrated snapshots")
	}
}

func TestReview8AbsentReadmeHasNoGrowthInfo(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	opts := &auditOptions{rootDir: f.dir, baseRef: "HEAD", maxStale: -1}
	out, err := captureStdout(t, func() error { return auditReadmePresentation(t.Context(), nil, opts) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "No baseline committed") {
		t.Errorf("absent README ran growth check: %s", out)
	}
}

func TestReview8ExplicitReadmeFile(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	findings, err := readmepresentation.CheckFile(t.Context(), f.dir, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	b := &baseline.Baseline{Version: 1, Infractions: readmepresentation.ToBaseline(findings)}
	path := filepath.Join(f.dir, separatedReadmeFile)
	if err := baseline.SaveBaseline(path, b); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runBaseline([]string{"--file", path, "--verify"}) })
	if err != nil {
		t.Fatalf("README --file rejected: %v", err)
	}
	mustContain(t, out, "1 active")
}

func TestReview8DebtLinesOnce(t *testing.T) {
	rep := &adopt.AdoptReport{BaselineStatus: "existing", BaselineRatchets: []adopt.BaselineRatchet{
		{File: separatedReadmeFile, Passed: true, Recorded: 1, Active: 1},
		{File: cliBaseline, Recorded: 3, Active: 5, Unbaselined: 2},
	}}
	out, err := captureStdout(t, func() error { printDebtSummary(rep); printAppliedOutcome(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, rep.BaselineRatchets[1].Line()) != 1 {
		t.Errorf("rejection duplicated or lost: %s", out)
	}
	if strings.Index(out, cliBaseline+")") > strings.Index(out, "recorded in "+separatedReadmeFile) {
		t.Errorf("unstable file order: %s", out)
	}
	rep.BaselineStatus = "failed"
	out, err = captureStdout(t, func() error { printDebtSummary(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "active infractions") {
		t.Errorf("ratchet printed after failed status: %s", out)
	}
}

func TestReview8AbsentEnforcedCleanReadme(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n[![a](https://img.shields.io/badge/a-b-blue?style=for-the-badge)](https://x)\n**Tag**\n")
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+"\nadoption:\n  decline: [dev-container, readme]\ngate:\n  readme_presentation: true\n")
	out, err := runBaselineCmd(t, f, "--verify")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ".readme-baseline.json: not recorded") {
		t.Errorf("clean unrecorded README has no notice: %s", out)
	}
	rep, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, LockSourceRoot: f.dir, RecordBaseline: true, SkipHookActivation: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { printDebtSummary(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ".readme-baseline.json: not recorded") {
		t.Errorf("clean unrecorded README has no notice: %s", out)
	}
	line, err := readmepresentation.Audit(t.Context(), readmepresentation.Options{Root: f.dir, Enforce: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(line, "within 0 recorded") {
		t.Errorf("absent enforced baseline passed ratchet: %s", line)
	}
	if !strings.Contains(line, ".readme-baseline.json: not recorded") {
		t.Errorf("clean audit has no unrecorded notice: %s", line)
	}
}
