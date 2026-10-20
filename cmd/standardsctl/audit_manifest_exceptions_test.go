// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// withException appends one exceptions entry to the fixture manifest: category and the days from
// today to its expiry decide whether config.ValidateExceptions accepts it.
func withException(t *testing.T, f *auditFixture, category string, days int) {
	t.Helper()
	today := time.Now()
	entry := fmt.Sprintf("exceptions:\n  - rule: HISS-10\n    path: .github/workflows/ci.yml\n    reason: planted entry\n"+
		"    category: %s\n    entered: %s\n    expires: %s\n", category, today.Format(time.DateOnly), today.AddDate(0, 0, days).Format(time.DateOnly))
	writeFixtureFile(t, f.dir, ".standards.yaml", readFixtureFile(t, f.dir, ".standards.yaml")+entry)
}

// Positive: a valid exception entry keeps the manifest step's PASS line.
func TestAuditManifest_Positive_ValidExceptionPasses(t *testing.T) {
	f := newAuditFixture(t)
	withException(t, f, "C", 10) // category C: expires exactly 10 days after entry
	out, err := f.audit(t)
	if !strings.Contains(out, "[PASS] Manifest verified") {
		t.Fatalf("no manifest PASS line (err %v):\n%s", err, out)
	}
}

// Negative (order engine-manifest-pass-on-refused-exception-20261019): an entry whose expiry is
// past its category's interval, or whose category is unknown, fails the manifest step itself,
// naming the entry, and no PASS line is printed for the manifest.
func TestAuditManifest_Negative_RefusedExceptionFailsTheManifestStep(t *testing.T) {
	for _, tc := range []struct {
		name, category string
		days           int
		want           string
	}{
		{"expiry past category D", "D", 200, "exceptions[0] category D requires expires"},
		{"unknown category", "E", 5, `exceptions[0] category "E" must be A, B, C or D`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFixture(t)
			withException(t, f, tc.category, tc.days)
			out, err := f.audit(t)
			mustErrContain(t, err, "[FAIL] Manifest audit failed")
			mustErrContain(t, err, tc.want)
			if strings.Contains(out, "[PASS] Manifest verified") {
				t.Fatalf("a refused exception entry still printed the manifest PASS line:\n%s", out)
			}
		})
	}
}
