// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package rules_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

var testToday = time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)

func TestCategoryDays_3D(t *testing.T) {
	cases := []struct {
		cat  string
		want int
		ok   bool
	}{
		{"A", 90, true},
		{"B", 3, true},
		{"C", 10, true},
		{"D", 120, true},
		{"E", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		days, ok := rules.CategoryDays(tc.cat)
		if ok != tc.ok || days != tc.want {
			t.Errorf("CategoryDays(%q) = (%d, %v), want (%d, %v)", tc.cat, days, ok, tc.want, tc.ok)
		}
	}
}

func TestExpiryProblem_3D(t *testing.T) {
	// Positive: within 90 days
	valid := testToday.AddDate(0, 0, 30).Format(rules.DateLayout)
	if prob := rules.ExpiryProblem(valid, testToday); prob != "" {
		t.Errorf("expected valid expiry %q, got: %s", valid, prob)
	}

	// Boundary: exactly 90 days
	boundary := testToday.AddDate(0, 0, 90).Format(rules.DateLayout)
	if prob := rules.ExpiryProblem(boundary, testToday); prob != "" {
		t.Errorf("expected boundary 90 days %q, got: %s", boundary, prob)
	}

	// Negative: 91 days
	pastBoundary := testToday.AddDate(0, 0, 91).Format(rules.DateLayout)
	if prob := rules.ExpiryProblem(pastBoundary, testToday); prob == "" {
		t.Errorf("expected error on 91 days, got empty")
	}

	// Negative: invalid date string
	if prob := rules.ExpiryProblem("not-a-date", testToday); prob == "" {
		t.Errorf("expected error on bad date string, got empty")
	}
}

func TestCategoryExpiryProblem_3D(t *testing.T) {
	// Category B: entered 2026-10-18 + 3 days = 2026-10-21
	entered := "2026-10-18"
	expB := "2026-10-21"
	if prob := rules.CategoryExpiryProblem("B", entered, expB, testToday); prob != "" {
		t.Errorf("expected valid B expiry, got: %s", prob)
	}

	// Category B mismatch
	if prob := rules.CategoryExpiryProblem("B", entered, "2026-10-22", testToday); prob == "" {
		t.Errorf("expected mismatch error on B, got empty")
	}

	// Unknown category
	if prob := rules.CategoryExpiryProblem("Z", entered, expB, testToday); prob == "" {
		t.Errorf("expected error on category Z, got empty")
	}

	// Uncategorized with entered
	if prob := rules.CategoryExpiryProblem("", entered, expB, testToday); prob != "entered requires a category" {
		t.Errorf("expected 'entered requires a category', got: %s", prob)
	}
}

func TestValidateHISS12Target_3D(t *testing.T) {
	// Positive
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "generic-api-key", "fp123", ""); prob != "" {
		t.Errorf("expected valid target, got: %s", prob)
	}

	// Negative: glob given
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "generic-api-key", "", "*.py"); prob == "" {
		t.Errorf("expected error on glob, got empty")
	}

	// Negative: missing path
	if prob := rules.ValidateHISS12Target("", "generic-api-key", "", ""); prob == "" {
		t.Errorf("expected error on missing path, got empty")
	}

	// Negative: glob in path
	if prob := rules.ValidateHISS12Target("pkg/*.go", "generic-api-key", "", ""); prob == "" {
		t.Errorf("expected error on glob in path, got empty")
	}

	// Negative: missing rule id
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "", "", ""); prob == "" {
		t.Errorf("expected error on missing rule id, got empty")
	}

	// Negative: whitespace in rule id
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "rule with spaces", "", ""); prob == "" {
		t.Errorf("expected error on whitespace in rule id, got empty")
	}

	// Boundary: 512 byte fingerprint
	boundaryFp := strings.Repeat("a", 512)
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "generic-api-key", boundaryFp, ""); prob != "" {
		t.Errorf("expected 512-byte fingerprint to pass, got: %s", prob)
	}
	if prob := rules.ValidateHISS12Target("pkg/secrets.go", "generic-api-key", boundaryFp+"a", ""); prob == "" {
		t.Errorf("expected 513-byte fingerprint to fail, got empty")
	}
}

func TestJudge_3D(t *testing.T) {
	finding := rules.Finding{
		File:        "creds.py",
		RuleID:      "generic-api-key",
		Fingerprint: "fp123",
		Commit:      "c1",
	}

	t.Run("positive unexpired entry excuses finding", func(t *testing.T) {
		entry := rules.Entry{
			Path:    "creds.py",
			RuleID:  "generic-api-key",
			Expires: "2026-11-01",
		}
		res := rules.Judge([]rules.Finding{finding}, []rules.Entry{entry}, "", testToday)
		if res.Failed() {
			t.Errorf("expected pass, got failed")
		}
		if len(res.Allowed) != 1 || len(res.Blocking) != 0 || len(res.Stale) != 0 || len(res.Expired) != 0 {
			t.Errorf("unexpected counts: %+v", res)
		}
	})

	t.Run("negative expired entry excuses nothing", func(t *testing.T) {
		entry := rules.Entry{
			Path:    "creds.py",
			RuleID:  "generic-api-key",
			Expires: "2026-10-01",
		}
		res := rules.Judge([]rules.Finding{finding}, []rules.Entry{entry}, "", testToday)
		if !res.Failed() {
			t.Errorf("expected failed, got pass")
		}
		if len(res.Blocking) != 1 || len(res.Expired) != 1 {
			t.Errorf("unexpected counts: %+v", res)
		}
	})

	t.Run("negative stale entry fails", func(t *testing.T) {
		entry := rules.Entry{
			Path:    "other.py",
			RuleID:  "generic-api-key",
			Expires: "2026-11-01",
		}
		res := rules.Judge([]rules.Finding{finding}, []rules.Entry{entry}, "", testToday)
		if !res.Failed() {
			t.Errorf("expected failed on stale, got pass")
		}
		if len(res.Stale) != 1 {
			t.Errorf("expected 1 stale entry, got %d", len(res.Stale))
		}
	})
}
