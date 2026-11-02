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
	cases := []struct{ name, category, entered, expires, want string }{
		{"A stated", "A", testToday.AddDate(0, 0, 0).Format(rules.DateLayout), testToday.AddDate(0, 0, 90).Format(rules.DateLayout), ""},
		{"A beyond 90 days", "A", testToday.AddDate(0, 0, 0).Format(rules.DateLayout), testToday.AddDate(0, 0, 91).Format(rules.DateLayout), "category A requires expires on or before " + testToday.AddDate(0, 0, 90).Format(rules.DateLayout) + " (90 days"},
		{"A missing expiry", "A", "", "", "must be a YYYY-MM-DD date"},
		{"B boundary", "B", "", testToday.AddDate(0, 0, 3).Format(rules.DateLayout), ""},
		{"B exceeded", "B", "", testToday.AddDate(0, 0, 4).Format(rules.DateLayout), "category B requires expires on or before " + testToday.AddDate(0, 0, 3).Format(rules.DateLayout) + " (3 days"},
		{"C shorter", "C", testToday.AddDate(0, 0, 0).Format(rules.DateLayout), testToday.AddDate(0, 0, 2).Format(rules.DateLayout), ""},
		{"D boundary", "D", "", testToday.AddDate(0, 0, 120).Format(rules.DateLayout), ""},
		{"D exceeded", "D", "", testToday.AddDate(0, 0, 121).Format(rules.DateLayout), "category D requires expires on or before " + testToday.AddDate(0, 0, 120).Format(rules.DateLayout) + " (120 days"},
		{"entered fixed", "C", testToday.AddDate(0, 0, -1).Format(rules.DateLayout), testToday.AddDate(0, 0, 10).Format(rules.DateLayout), "category C requires expires on or before " + testToday.AddDate(0, 0, 9).Format(rules.DateLayout) + " (10 days"},
		{"bad entered", "B", "bad", testToday.AddDate(0, 0, 3).Format(rules.DateLayout), "entered"},
		{"bad expiry", "B", testToday.AddDate(0, 0, 0).Format(rules.DateLayout), "bad", "must be a YYYY-MM-DD date"},
		{"uncategorized boundary", "", testToday.AddDate(0, 0, -1).Format(rules.DateLayout), testToday.AddDate(0, 0, 89).Format(rules.DateLayout), ""},
		{"uncategorized exceeded", "", testToday.AddDate(0, 0, -1).Format(rules.DateLayout), testToday.AddDate(0, 0, 90).Format(rules.DateLayout), "more than 90 days"},
		{"entered before cutoff without category", "", testToday.AddDate(0, 0, 10).Format(rules.DateLayout), testToday.AddDate(0, 0, 100).Format(rules.DateLayout), ""},
		{"entered cutoff without category", "", testToday.AddDate(0, 0, 11).Format(rules.DateLayout), testToday.AddDate(0, 0, 14).Format(rules.DateLayout), "category is required"},
		{"entered cutoff with category B", "B", testToday.AddDate(0, 0, 11).Format(rules.DateLayout), testToday.AddDate(0, 0, 14).Format(rules.DateLayout), ""},
		{"entered cutoff category B exceeded", "B", testToday.AddDate(0, 0, 11).Format(rules.DateLayout), testToday.AddDate(0, 0, 15).Format(rules.DateLayout), "category B requires expires on or before " + testToday.AddDate(0, 0, 14).Format(rules.DateLayout) + " (3 days"},
		{"no entered grace boundary", "", "", testToday.AddDate(0, 0, 41).Format(rules.DateLayout), ""},
		{"no entered past grace", "", "", testToday.AddDate(0, 0, 42).Format(rules.DateLayout), "category is required"},
	}
	for _, tc := range cases {
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
