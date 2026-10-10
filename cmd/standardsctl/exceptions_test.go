// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func writeExceptionsManifest(t *testing.T, entries ...config.Exception) string {
	t.Helper()
	root := t.TempDir()
	manifestPath := filepath.Join(root, config.ManifestFileName)
	var sb strings.Builder
	sb.WriteString("version: 1\nexceptions:\n")
	for _, e := range entries {
		fmt.Fprintf(&sb, "  - rule: %q\n    path: %q\n    reason: %q\n    expires: %q\n",
			e.Rule, e.Path, e.Reason, e.Expires)
	}
	if err := os.WriteFile(manifestPath, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath
}

func TestExceptionsList_Positive_ListsEntries(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	manifest := writeExceptionsManifest(t,
		config.Exception{Rule: "HISS-11", Path: ".github/workflows/rel.yml", Reason: "Level 2 builder", Expires: "2026-10-24"},
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/win.c", Reason: "Windows only", Expires: "2026-11-09"},
	)

	out, err := captureStdout(t, func() error {
		return dispatchCommand("exceptions", []string{"list", "--config=" + manifest, "--today=" + today.Format(config.ExceptionDateLayout)})
	})
	if err != nil {
		t.Fatalf("exceptions list failed: %v", err)
	}
	mustContain(t, out,
		"=== Declared Exceptions ("+manifest+") ===",
		"rule: HISS-11, path: .github/workflows/rel.yml, reason: Level 2 builder (14 days left)",
		"rule: clang-tidy-coverage, path: src/win.c, reason: Windows only (30 days left)",
	)
}

func TestExceptionsList_Positive_FiltersByExpiringWithin(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	manifest := writeExceptionsManifest(t,
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/a.c", Reason: "first debt", Expires: "2026-10-15"},
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/b.c", Reason: "second debt", Expires: "2026-11-09"},
	)

	out, err := captureStdout(t, func() error {
		return dispatchCommand("exceptions", []string{"list", "--expiring-within=14", "--config=" + manifest, "--today=" + today.Format(config.ExceptionDateLayout)})
	})
	if err != nil {
		t.Fatalf("exceptions list with filter failed: %v", err)
	}
	mustContain(t, out, "src/a.c", "5 days left")
	if strings.Contains(out, "src/b.c") {
		t.Errorf("output unexpectedly contained src/b.c (30 days ahead): %s", out)
	}
}

func TestExceptionsList_Boundary_ExactDaysAndZero(t *testing.T) {
	today := time.Date(2026, time.October, 10, 0, 0, 0, 0, time.UTC)
	manifest := writeExceptionsManifest(t,
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/at-14.c", Reason: "exact boundary", Expires: "2026-10-24"},
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/at-15.c", Reason: "one over boundary", Expires: "2026-10-25"},
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/at-0.c", Reason: "expires today", Expires: "2026-10-10"},
	)

	out, err := captureStdout(t, func() error {
		return dispatchCommand("exceptions", []string{"list", "--expiring-within=14", "--config=" + manifest, "--today=" + today.Format(config.ExceptionDateLayout)})
	})
	if err != nil {
		t.Fatalf("exceptions list boundary failed: %v", err)
	}
	mustContain(t, out, "src/at-14.c", "14 days left", "src/at-0.c", "0 days left")
	if strings.Contains(out, "src/at-15.c") {
		t.Errorf("output unexpectedly contained src/at-15.c: %s", out)
	}
}

func TestExceptionsList_Boundary_EmptyAndNoMatches(t *testing.T) {
	manifest := writeExceptionsManifest(t)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("exceptions", []string{"list", "--config=" + manifest})
	})
	if err != nil {
		t.Fatalf("exceptions list empty manifest failed: %v", err)
	}
	mustContain(t, out, "No exceptions declared.")

	manifestFuture := writeExceptionsManifest(t,
		config.Exception{Rule: "clang-tidy-coverage", Path: "src/future.c", Reason: "future debt", Expires: "2026-12-01"},
	)
	today := "2026-10-10"
	outFilter, err := captureStdout(t, func() error {
		return dispatchCommand("exceptions", []string{"list", "--expiring-within=7", "--config=" + manifestFuture, "--today=" + today})
	})
	if err != nil {
		t.Fatalf("exceptions list no matches failed: %v", err)
	}
	mustContain(t, outFilter, "No exceptions expiring within 7 days.")
}

func TestExceptionsList_Negative_InvalidArgs(t *testing.T) {
	// Negative: negative days
	err := dispatchCommand("exceptions", []string{"list", "--expiring-within=-2"})
	mustErrContain(t, err, "--expiring-within must be non-negative")

	// Negative: unknown subcommand
	err = dispatchCommand("exceptions", []string{"bogus"})
	mustErrContain(t, err, "unknown exceptions action: bogus")

	// Negative: too many arguments
	err = dispatchCommand("exceptions", []string{"list", "extra"})
	mustErrContain(t, err, "exceptions accepts at most one action")

	// Negative: missing manifest
	err = dispatchCommand("exceptions", []string{"list", "--config=/nonexistent/path/.standards.yaml"})
	mustErrContain(t, err, "load manifest")

	// Negative: invalid --today
	err = dispatchCommand("exceptions", []string{"list", "--today=invalid-date"})
	mustErrContain(t, err, "--today \"invalid-date\" must be a YYYY-MM-DD date")
}
