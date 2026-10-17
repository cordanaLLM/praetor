// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestAuditExceptionsExpiry_3D(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	// The entries name files that exist, so only the expiry warning is under test here.
	root := exceptionTargetRoot(t, ".github/workflows/rel.yml", "boundary-14.yml", "boundary-15.yml", "boundary-0.yml",
		"expired.yml", "future.yml")

	// Positive: exception expiring within 14 days produces warning
	manifestPos := &config.Manifest{
		Exceptions: []config.Exception{
			{
				Rule:    "HISS-11",
				Path:    ".github/workflows/rel.yml",
				Reason:  "debt due soon",
				Expires: "2026-10-17", // 7 days left
			},
		},
	}
	out, err := captureStdout(t, func() error {
		return auditExceptionsExpiry(context.Background(), manifestPos, root, today)
	})
	if err != nil {
		t.Fatalf("auditExceptionsExpiry failed: %v", err)
	}
	mustContain(t, out,
		"[WARN] Exceptions: the exceptions entry (rule HISS-11, .github/workflows/rel.yml) expires on 2026-10-17 (7 days left): debt due soon",
	)

	// Boundary: exactly 14 days, 15 days, 0 days
	manifestBoundary := &config.Manifest{
		Exceptions: []config.Exception{
			{
				Rule:    "HISS-11",
				Path:    "boundary-14.yml",
				Reason:  "at 14 days",
				Expires: "2026-10-24", // 14 days
			},
			{
				Rule:    "HISS-11",
				Path:    "boundary-15.yml",
				Reason:  "at 15 days",
				Expires: "2026-10-25", // 15 days
			},
			{
				Rule:    "HISS-11",
				Path:    "boundary-0.yml",
				Reason:  "at 0 days",
				Expires: "2026-10-10", // 0 days
			},
		},
	}
	out, err = captureStdout(t, func() error {
		return auditExceptionsExpiry(context.Background(), manifestBoundary, root, today)
	})
	if err != nil {
		t.Fatalf("auditExceptionsExpiry boundary failed: %v", err)
	}
	mustContain(t, out,
		"(rule HISS-11, boundary-14.yml) expires on 2026-10-24 (14 days left): at 14 days",
		"(rule HISS-11, boundary-0.yml) expires on 2026-10-10 (0 days left): at 0 days",
	)
	if strings.Contains(out, "boundary-15.yml") {
		t.Errorf("output unexpectedly contained boundary-15.yml: %s", out)
	}

	// Negative: expired entry (-1 days) and distant future entry (30 days)
	manifestNeg := &config.Manifest{
		Exceptions: []config.Exception{
			{
				Rule:    "HISS-11",
				Path:    "expired.yml",
				Reason:  "already expired",
				Expires: "2026-10-09", // -1 days
			},
			{
				Rule:    "HISS-11",
				Path:    "future.yml",
				Reason:  "future entry",
				Expires: "2026-11-09", // 30 days
			},
		},
	}
	out, err = captureStdout(t, func() error {
		return auditExceptionsExpiry(context.Background(), manifestNeg, root, today)
	})
	if err != nil {
		t.Fatalf("auditExceptionsExpiry negative failed: %v", err)
	}
	if strings.Contains(out, "[WARN] Exceptions:") {
		t.Errorf("expected no warning for expired or distant entries, got: %s", out)
	}

	// Negative: nil manifest
	out, err = captureStdout(t, func() error {
		return auditExceptionsExpiry(context.Background(), nil, root, today)
	})
	if err != nil || out != "" {
		t.Errorf("nil manifest want (empty, nil), got (%q, %v)", out, err)
	}
}

func TestAudit_ExceptionsExpiryWarning_Integration(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/build.yml", fixtureNativeWorkflow(""))

	// Add an exception expiring in 10 days
	expires := time.Now().AddDate(0, 0, 10).Format(config.ExceptionDateLayout)
	entry := fmt.Sprintf("exceptions:\n  - rule: HISS-10\n    path: .github/workflows/build.yml\n    reason: compiler upgrade pending\n    expires: %q\n", expires)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+entry)

	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed unexpectedly: %v\nOutput: %s", err, out)
	}
	mustContain(t, out,
		fmt.Sprintf("[WARN] Exceptions: the exceptions entry (rule HISS-10, .github/workflows/build.yml) expires on %s (10 days left): compiler upgrade pending", expires),
		"Audit Summary: configured governance gates passed",
	)

	// In contrast, an exception expiring in 30 days produces no warning in audit
	expires30 := time.Now().AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	entry30 := fmt.Sprintf("exceptions:\n  - rule: HISS-10\n    path: .github/workflows/build.yml\n    reason: compiler upgrade pending\n    expires: %q\n", expires30)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+entry30)

	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed unexpectedly: %v\nOutput: %s", err, out)
	}
	if strings.Contains(out, "[WARN] Exceptions:") {
		t.Errorf("audit unexpectedly warned for entry expiring in 30 days:\n%s", out)
	}
}

func TestAuditExceptionsExpiry_CategoryWarningWindows_3D(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	root := exceptionTargetRoot(t, "cat-c-fresh.yml", "cat-c-5.yml", "cat-c-6.yml", "cat-d-3.yml",
		"cat-b-fresh.yml", "cat-b-1.yml", "uncat-14.yml")

	t.Run("category C entered today does not warn", func(t *testing.T) {
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-c-fresh.yml",
			Category: "C",
			Entered:  "2026-10-10",
			Expires:  "2026-10-20",
			Reason:   "fresh category C",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		if strings.Contains(out, "[WARN] Exceptions:") {
			t.Errorf("expected no warning for fresh category C entry, got: %s", out)
		}
	})

	t.Run("category C with 5 days left warns", func(t *testing.T) {
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-c-5.yml",
			Category: "C",
			Entered:  "2026-10-05",
			Expires:  "2026-10-15",
			Reason:   "category C 5 days left",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		mustContain(t, out, "(rule HISS-11, cat-c-5.yml) expires on 2026-10-15 (5 days left): category C 5 days left")
	})

	t.Run("category C with 6 days left does not warn", func(t *testing.T) {
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-c-6.yml",
			Category: "C",
			Entered:  "2026-10-06",
			Expires:  "2026-10-16",
			Reason:   "category C 6 days left",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		if strings.Contains(out, "[WARN] Exceptions:") {
			t.Errorf("expected no warning for category C entry with 6 days left, got: %s", out)
		}
	})

	t.Run("category D with 3 days left warns", func(t *testing.T) {
		enteredD := today.AddDate(0, 0, -117).Format(config.ExceptionDateLayout)
		expiresD := today.AddDate(0, 0, 3).Format(config.ExceptionDateLayout)
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-d-3.yml",
			Category: "D",
			Entered:  enteredD,
			Expires:  expiresD,
			Reason:   "category D 3 days left",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		mustContain(t, out, fmt.Sprintf("(rule HISS-11, cat-d-3.yml) expires on %s (3 days left): category D 3 days left", expiresD))
	})

	t.Run("category B entered today does not warn", func(t *testing.T) {
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-b-fresh.yml",
			Category: "B",
			Entered:  "2026-10-10",
			Expires:  "2026-10-13",
			Reason:   "fresh category B",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		if strings.Contains(out, "[WARN] Exceptions:") {
			t.Errorf("expected no warning for fresh category B entry, got: %s", out)
		}
	})

	t.Run("category B with 1 day left warns", func(t *testing.T) {
		entry := config.Exception{
			Rule:     "HISS-11",
			Path:     "cat-b-1.yml",
			Category: "B",
			Entered:  "2026-10-08",
			Expires:  "2026-10-11",
			Reason:   "category B 1 day left",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		mustContain(t, out, "(rule HISS-11, cat-b-1.yml) expires on 2026-10-11 (1 day left): category B 1 day left")
	})

	t.Run("uncategorized with 14 days left warns", func(t *testing.T) {
		entry := config.Exception{
			Rule:    "HISS-11",
			Path:    "uncat-14.yml",
			Expires: "2026-10-24",
			Reason:  "uncategorized 14 days left",
		}
		out, err := captureStdout(t, func() error {
			return auditExceptionsExpiry(context.Background(), &config.Manifest{Exceptions: []config.Exception{entry}}, root, today)
		})
		if err != nil {
			t.Fatalf("auditExceptionsExpiry failed: %v", err)
		}
		mustContain(t, out, "(rule HISS-11, uncat-14.yml) expires on 2026-10-24 (14 days left): uncategorized 14 days left")
	})
}
