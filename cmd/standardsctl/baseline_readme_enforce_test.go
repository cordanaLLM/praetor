// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// TestAuditAndBaselineVerifyRatchetOnClaimedTotal pins the ratchet on the claimed per-class total, as main does:
// a baseline whose claimed total (0) is below its entries (1) sees the matching violation as a count rise and
// refuses it, never trusting the larger entry count (the never-max rule).
func TestAuditAndBaselineVerifyRatchetOnClaimedTotal(t *testing.T) {
	f := newAuditFixture(t)
	inf := f.addViolation(t)
	f.writeBaseline(t, []baseline.Infraction{inf}, "")
	before := readFixtureFile(t, f.dir, ".standards-baseline.json")
	understated := strings.Replace(before, `"total_infractions": 1`, `"total_infractions": 0`, 1)
	if understated == before {
		t.Fatal("fixture did not lower the claimed total")
	}
	writeFixtureFile(t, f.dir, ".standards-baseline.json", understated)
	gitCommitAll(t, f.dir, f.gitEnv, "understated baseline fixture")
	_, auditErr := f.audit(t, "--offline")
	_, verifyErr := runBaselineCmd(t, f, "--verify")
	for _, result := range []struct {
		name string
		err  error
	}{{"audit --offline", auditErr}, {"baseline --verify", verifyErr}} {
		if result.err == nil {
			t.Errorf("%s accepted a count above the claimed total 0; the ratchet must judge the claimed total", result.name)
		}
	}
	if after := readFixtureFile(t, f.dir, ".standards-baseline.json"); after != understated {
		t.Error("read-only audit or verify rewrote the baseline")
	}
}

func TestBaselineVerifyReadmeEnforcement(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, "README.md", "# Widgets\n")
	if out, err := runBaselineCmd(t, f, "--verify"); err != nil {
		t.Fatalf("report-only verify: %v\n%s", err, out)
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", "version: 1\ngate:\n  readme_presentation: true\n")
	if _, err := runBaselineCmd(t, f, "--verify"); err == nil || !strings.Contains(err.Error(), "README presentation findings") {
		t.Fatalf("enforced verify ignored new README debt: %v", err)
	}
}

func TestBaselineInspectUsesFileCounts(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".readme-baseline.json",
		`{"version":1,"total_infractions":0,"infractions":[{"rule_id":"README-01"}]}`)
	out, err := runBaselineCmd(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Total Infractions: 1")
	if strings.Contains(out, "Zero technical debt recorded in .readme-baseline.json") {
		t.Error("inspection ignored the README count")
	}
}
