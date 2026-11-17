// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
	"time"
)

// TestNodeAuditExceptionsValidation checks the shared schema's positive, negative and date boundaries.
func TestNodeAuditExceptionsValidation(t *testing.T) {
	today := time.Now().UTC()
	good := "version: 1\nexceptions:\n  - rule: node-audit\n    path: package.json\n    ghsa: GHSA-fc5q-39xc-vjpf\n    reason: Await the dependency fix\n    category: C\n    entered: " + today.Format(ExceptionDateLayout) + "\n    expires: " + today.AddDate(0, 0, 10).Format(ExceptionDateLayout) + "\n"
	cases := []struct {
		name, from, to string
		valid          bool
	}{
		{"live", "", "", true},
		{"bad id", "GHSA-fc5q-39xc-vjpf", "CVE-2026-1234", false},
		{"partial id", "GHSA-fc5q-39xc-vjpf", "GHSA-fc5q-39xc", false},
		{"missing id", "    ghsa: GHSA-fc5q-39xc-vjpf\n", "", false},
		{"missing category", "    category: C\n", "", false},
		{"missing entered", "    entered: " + today.Format(ExceptionDateLayout) + "\n", "", false},
		{"wrong path", "path: package.json", "path: other.json", false},
		{"glob", "path: package.json", "glob: '*.json'", false},
		{"other rule", "rule: node-audit", "rule: HISS-19", false},
		{"category cap", "category: C", "category: B", false},
		{"missing expiry", "    expires: " + today.AddDate(0, 0, 10).Format(ExceptionDateLayout) + "\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := good
			if tc.from != "" {
				text = strings.Replace(text, tc.from, tc.to, 1)
			}
			_, err := LoadManifest(writeManifest(t, text))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

// TestNodeAuditExceptionForDates checks exact GHSA matching, inclusive expiry and expired evidence.
func TestNodeAuditExceptionForDates(t *testing.T) {
	today := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	entry := Exception{Rule: ExceptionRuleNodeAudit, Path: "package.json", GHSA: "GHSA-fc5q-39xc-vjpf", Expires: "2026-11-17"}
	live, expired := NodeAuditExceptionFor([]Exception{entry}, entry.GHSA, today)
	if live == nil || expired != nil {
		t.Fatalf("expiry day: live=%v expired=%v", live, expired)
	}
	live, expired = NodeAuditExceptionFor([]Exception{entry}, entry.GHSA, today.AddDate(0, 0, 1))
	if live != nil || expired == nil {
		t.Fatalf("expired: live=%v expired=%v", live, expired)
	}
	live, expired = NodeAuditExceptionFor([]Exception{entry}, "GHSA-42xw-2xvc-qx8m", today)
	if live != nil || expired != nil {
		t.Fatalf("different id matched: live=%v expired=%v", live, expired)
	}
}

// TestNodeAuditExceptionKeys allows distinct advisories for one manifest and rejects exact duplicates.
func TestNodeAuditExceptionKeys(t *testing.T) {
	today := time.Now().UTC()
	entry := Exception{Rule: ExceptionRuleNodeAudit, Path: "package.json", GHSA: "GHSA-fc5q-39xc-vjpf", Reason: "Await dependency fix", Category: "C", Entered: today.Format(ExceptionDateLayout), Expires: today.AddDate(0, 0, 10).Format(ExceptionDateLayout)}
	other := entry
	other.GHSA = "GHSA-42xw-2xvc-qx8m"
	if err := ValidateExceptions([]Exception{entry, other}, today); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExceptions([]Exception{entry, entry}, today); err == nil {
		t.Fatal("duplicate GHSA exception accepted")
	}
}
