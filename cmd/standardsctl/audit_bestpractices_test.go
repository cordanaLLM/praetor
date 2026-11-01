// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import "testing"

func TestAuditBestPracticesClaims_CLI(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed without .bestpractices.json: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[SKIP] bestpractices-claims: no .bestpractices.json found; not applicable.")

	writeFixtureFile(t, f.dir, ".bestpractices.json",
		"{\n  \"test_policy_status\": \"Unmet\",\n  \"test_policy_justification\": \"No automated test suite applies to an organisation profile repository.\"\n}\n")
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on valid .bestpractices.json: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] bestpractices-claims: .bestpractices.json verified (1 criterion claim(s) checked against OpenSSF Best Practices criteria@8b3a2889e3af).")

	writeFixtureFile(t, f.dir, ".bestpractices.json",
		"{\n  \"description_good_status\": \"N/A\"\n}\n")
	if out, err = f.audit(t); err == nil {
		t.Fatalf("audit passed on forbidden N/A claim:\n%s", out)
	}
	mustErrContain(t, err, `[FAIL] bestpractices-claims: .bestpractices.json has 1 forbidden claim(s): criterion "description_good" (status "N/A", rule na_not_allowed)`)
}
