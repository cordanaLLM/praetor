// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateExceptionsCategoryCaps(t *testing.T) {
	today := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	cases := []struct{ name, category, reason, entered, expires, want string }{
		{"D 120 days", "D", "gap", "", "2027-02-18", ""},
		{"D 121 days", "D", "gap", "", "2027-02-19", "category D requires expires on or before 2027-02-18 (120 days"},
		{"C 10 days", "C", "gap", "", "2026-10-31", ""},
		{"C 11 days", "C", "gap", "", "2026-11-01", "category C requires expires on or before 2026-10-31 (10 days"},
		{"B 3 days", "B", "gap", "", "2026-10-24", ""},
		{"B 4 days", "B", "gap", "", "2026-10-25", "category B requires expires on or before 2026-10-24 (3 days"},
		{"reason D", "", "category D: upstream boilerplate", "", "2027-02-18", ""},
		{"reason mixed case", "", "CaTeGoRy d, upstream boilerplate", "", "2027-02-18", ""},
		{"reason D exceeded", "", "category D: gap", "", "2027-02-19", "category D requires expires on or before 2027-02-18 (120 days"},
		{"field wins", "B", "category D: gap", "", "2026-10-25", "category B"},
		{"uncategorized 90 days", "", "gap", "", "2027-01-19", ""},
		{"uncategorized 91 days", "", "gap", "", "2027-01-20", "more than 90 days"},
		{"embedded category ignored", "", "gap category D", "", "2027-02-18", "more than 90 days"},
		{"noncategory word ignored", "", "category Debt", "", "2027-02-18", "more than 90 days"},
		{"entered D boundary", "D", "gap", "2026-10-20", "2027-02-17", ""},
		{"entered D exceeded", "D", "gap", "2026-10-20", "2027-02-18", "category D requires expires on or before 2027-02-17 (120 days"},
		{"shorter interval", "C", "gap", "2026-10-21", "2026-10-25", ""},
		{"entered uncategorized", "", "gap", "2026-10-20", "2027-01-18", ""},
		{"entered uncategorized exceeded", "", "gap", "2026-10-20", "2027-01-19", "more than 90 days"},
		{"A stated date", "A", "gap", "2026-10-21", "2027-01-19", ""},
		{"A beyond 90 days", "A", "gap", "2026-10-21", "2027-01-20", "category A requires expires on or before 2027-01-19 (90 days"},
		{"A no entered", "A", "gap", "", "2027-01-19", ""},
		{"A missing expiry", "A", "gap", "", "", "must be a YYYY-MM-DD date"},
		{"invalid entered", "D", "gap", "yesterday", "2027-02-18", "entered"},
		{"invalid expiry", "D", "gap", "", "tomorrow", "must be a YYYY-MM-DD date"},
		{"invalid category", "Z", "category D: gap", "", "2027-02-18", "must be A, B, C or D"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := validException()
			entry.Category, entry.Reason, entry.Entered, entry.Expires = tc.category, tc.reason, tc.entered, tc.expires
			err := ValidateExceptions([]Exception{entry}, today)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExceptionWarningDaysReasonCategory(t *testing.T) {
	entry := Exception{Reason: "category C: gap"}
	if got := entry.WarningDays(); got != 5 {
		t.Fatalf("reason category warning = %d, want 5", got)
	}
	entry.Category = "B"
	if got := entry.WarningDays(); got != 1 {
		t.Fatalf("explicit category warning = %d, want 1", got)
	}
}
