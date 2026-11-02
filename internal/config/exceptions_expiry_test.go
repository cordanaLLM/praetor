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
		{"D 120 days", "D", "gap", "", today.AddDate(0, 0, 120).Format(ExceptionDateLayout), ""},
		{"D 121 days", "D", "gap", "", today.AddDate(0, 0, 121).Format(ExceptionDateLayout), "category D requires expires on or before " + today.AddDate(0, 0, 120).Format(ExceptionDateLayout) + " (120 days"},
		{"C 10 days", "C", "gap", "", today.AddDate(0, 0, 10).Format(ExceptionDateLayout), ""},
		{"C 11 days", "C", "gap", "", today.AddDate(0, 0, 11).Format(ExceptionDateLayout), "category C requires expires on or before " + today.AddDate(0, 0, 10).Format(ExceptionDateLayout) + " (10 days"},
		{"B 3 days", "B", "gap", "", today.AddDate(0, 0, 3).Format(ExceptionDateLayout), ""},
		{"B 4 days", "B", "gap", "", today.AddDate(0, 0, 4).Format(ExceptionDateLayout), "category B requires expires on or before " + today.AddDate(0, 0, 3).Format(ExceptionDateLayout) + " (3 days"},
		{"reason D", "", "category D: upstream boilerplate", "", today.AddDate(0, 0, 120).Format(ExceptionDateLayout), ""},
		{"reason mixed case", "", "CaTeGoRy d, upstream boilerplate", "", today.AddDate(0, 0, 120).Format(ExceptionDateLayout), ""},
		{"reason D exceeded", "", "category D: gap", "", today.AddDate(0, 0, 121).Format(ExceptionDateLayout), "category D requires expires on or before " + today.AddDate(0, 0, 120).Format(ExceptionDateLayout) + " (120 days"},
		{"field wins", "B", "category D: gap", "", today.AddDate(0, 0, 4).Format(ExceptionDateLayout), "category B"},
		{"uncategorized no entered grace boundary", "", "gap", "", today.AddDate(0, 0, 38).Format(ExceptionDateLayout), ""},
		{"uncategorized no entered past grace", "", "gap", "", today.AddDate(0, 0, 39).Format(ExceptionDateLayout), "category is required (entered is omitted and expires " + today.AddDate(0, 0, 39).Format(ExceptionDateLayout) + " is after " + today.AddDate(0, 0, 38).Format(ExceptionDateLayout)},
		{"embedded category ignored", "", "gap category D", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 120).Format(ExceptionDateLayout), "more than 90 days"},
		{"noncategory word ignored", "", "category Debt", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 120).Format(ExceptionDateLayout), "more than 90 days"},
		{"entered D boundary", "D", "gap", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 119).Format(ExceptionDateLayout), ""},
		{"entered D exceeded", "D", "gap", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 120).Format(ExceptionDateLayout), "category D requires expires on or before " + today.AddDate(0, 0, 119).Format(ExceptionDateLayout) + " (120 days"},
		{"shorter interval", "C", "gap", today.AddDate(0, 0, 0).Format(ExceptionDateLayout), today.AddDate(0, 0, 4).Format(ExceptionDateLayout), ""},
		{"entered uncategorized", "", "gap", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 89).Format(ExceptionDateLayout), ""},
		{"entered uncategorized exceeded", "", "gap", today.AddDate(0, 0, -1).Format(ExceptionDateLayout), today.AddDate(0, 0, 90).Format(ExceptionDateLayout), "more than 90 days"},
		{"entered before cutoff uncategorized", "", "gap", today.AddDate(0, 0, 7).Format(ExceptionDateLayout), today.AddDate(0, 0, 90).Format(ExceptionDateLayout), ""},
		{"entered cutoff without category", "", "gap", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 11).Format(ExceptionDateLayout), "category is required (entered " + today.AddDate(0, 0, 8).Format(ExceptionDateLayout) + " is on or after " + today.AddDate(0, 0, 8).Format(ExceptionDateLayout)},
		{"entered cutoff with field category", "B", "gap", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 11).Format(ExceptionDateLayout), ""},
		{"entered cutoff with B + 4", "B", "gap", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 12).Format(ExceptionDateLayout), "category B requires expires on or before " + today.AddDate(0, 0, 11).Format(ExceptionDateLayout) + " (3 days"},
		{"entered cutoff with reason prefix category", "", "category B: gap", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 11).Format(ExceptionDateLayout), ""},
		{"A stated date", "A", "gap", today.AddDate(0, 0, 0).Format(ExceptionDateLayout), today.AddDate(0, 0, 90).Format(ExceptionDateLayout), ""},
		{"A beyond 90 days", "A", "gap", today.AddDate(0, 0, 0).Format(ExceptionDateLayout), today.AddDate(0, 0, 91).Format(ExceptionDateLayout), "category A requires expires on or before " + today.AddDate(0, 0, 90).Format(ExceptionDateLayout) + " (90 days"},
		{"A no entered", "A", "gap", "", today.AddDate(0, 0, 90).Format(ExceptionDateLayout), ""},
		{"A missing expiry", "A", "gap", "", "", "must be a YYYY-MM-DD date"},
		{"invalid entered", "D", "gap", "yesterday", today.AddDate(0, 0, 120).Format(ExceptionDateLayout), "entered"},
		{"invalid expiry", "D", "gap", "", "tomorrow", "must be a YYYY-MM-DD date"},
		{"invalid category", "Z", "category D: gap", "", today.AddDate(0, 0, 120).Format(ExceptionDateLayout), "must be A, B, C or D"},
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

func TestValidateExceptionsHISS18CategoryCutoff(t *testing.T) {
	today := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		name, category, entered, expires, want string
	}{
		{"positive categorized past cutoff", "A", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 71).Format(ExceptionDateLayout), ""},
		{"boundary uncategorized entered before cutoff", "", today.AddDate(0, 0, 7).Format(ExceptionDateLayout), today.AddDate(0, 0, 90).Format(ExceptionDateLayout), ""},
		{"boundary uncategorized without entered within grace", "", "", today.AddDate(0, 0, 38).Format(ExceptionDateLayout), ""},
		{"negative uncategorized entered on cutoff", "", today.AddDate(0, 0, 8).Format(ExceptionDateLayout), today.AddDate(0, 0, 11).Format(ExceptionDateLayout), "category is required (entered " + today.AddDate(0, 0, 8).Format(ExceptionDateLayout) + " is on or after " + today.AddDate(0, 0, 8).Format(ExceptionDateLayout)},
		{"negative uncategorized without entered past grace", "", "", today.AddDate(0, 0, 39).Format(ExceptionDateLayout), "category is required (entered is omitted and expires " + today.AddDate(0, 0, 39).Format(ExceptionDateLayout) + " is after " + today.AddDate(0, 0, 38).Format(ExceptionDateLayout)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := Exception{
				Rule:     ExceptionRuleWorkflowTriggers,
				Path:     ".github/workflows/release.yml",
				Category: tc.category,
				Entered:  tc.entered,
				Reason:   "workflow must run on every branch push",
				Expires:  tc.expires,
			}
			err := ValidateExceptions([]Exception{entry}, today)
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("ValidateExceptions = %v, want %q", err, tc.want)
			}
		})
	}
}
