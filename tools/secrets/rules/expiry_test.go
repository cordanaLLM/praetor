// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package rules_test

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

func TestCategoryExpiryCaps(t *testing.T) {
	for _, tc := range []struct{ name, category, entered, expires, want string }{
		{"A stated", "A", "2026-10-18", "2027-01-16", ""},
		{"A beyond 90 days", "A", "2026-10-18", "2027-01-17", "category A requires expires on or before 2027-01-16 (90 days"},
		{"A missing expiry", "A", "", "", "must be a YYYY-MM-DD date"},
		{"B boundary", "B", "", "2026-10-21", ""},
		{"B exceeded", "B", "", "2026-10-22", "category B requires expires on or before 2026-10-21 (3 days"},
		{"C shorter", "C", "2026-10-18", "2026-10-20", ""},
		{"D boundary", "D", "", "2027-02-15", ""},
		{"D exceeded", "D", "", "2027-02-16", "category D requires expires on or before 2027-02-15 (120 days"},
		{"entered fixed", "C", "2026-10-17", "2026-10-28", "category C requires expires on or before 2026-10-27 (10 days"},
		{"bad entered", "B", "bad", "2026-10-21", "entered"},
		{"bad expiry", "B", "2026-10-18", "bad", "must be a YYYY-MM-DD date"},
		{"uncategorized boundary", "", "2026-10-17", "2027-01-15", ""},
		{"uncategorized exceeded", "", "2026-10-17", "2027-01-16", "more than 90 days"},
		{"entered before cutoff without category", "", "2026-10-28", "2027-01-26", ""},
		{"entered cutoff without category", "", "2026-10-29", "2026-11-01", "category is required"},
		{"entered cutoff with category B", "B", "2026-10-29", "2026-11-01", ""},
		{"entered cutoff category B exceeded", "B", "2026-10-29", "2026-11-02", "category B requires expires on or before 2026-11-01 (3 days"},
		{"no entered grace boundary", "", "", "2026-11-28", ""},
		{"no entered past grace", "", "", "2026-11-29", "category is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := rules.CategoryExpiryProblem(tc.category, tc.entered, tc.expires, testToday)
			if tc.want == "" {
				if got != "" {
					t.Fatal(got)
				}
			} else if !strings.Contains(got, tc.want) {
				t.Fatalf("problem = %q, want %q", got, tc.want)
			}
		})
	}
}
