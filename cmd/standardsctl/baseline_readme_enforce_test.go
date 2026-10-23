// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

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
