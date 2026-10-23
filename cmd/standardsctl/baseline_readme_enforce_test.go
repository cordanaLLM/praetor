// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

func TestAuditAndBaselineVerifyRejectUnderstatedTotal(t *testing.T) {
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
	t.Logf("understated total: audit refused=%t; baseline --verify refused=%t", auditErr != nil, verifyErr != nil)
	for _, result := range []struct {
		name string
		err  error
	}{{"audit --offline", auditErr}, {"baseline --verify", verifyErr}} {
		if result.err == nil || !strings.Contains(result.err.Error(), "total infractions rose from 0 to 1") {
			t.Errorf("%s must refuse the understated total with count regression: %v", result.name, result.err)
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

func TestBaselineInspectUsesClassCounts(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".standards-baseline.json",
		`{"version":1,"total_infractions":0,"infractions":[{"rule_id":"README-01"}]}`)
	out, err := runBaselineCmd(t, f)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Total Infractions: 1")
	if strings.Contains(out, "Zero technical debt") {
		t.Error("inspection ignored the README count")
	}
}
