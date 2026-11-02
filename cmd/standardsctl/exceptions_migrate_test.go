// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestWriteExceptionMigrationSafeMode pins removal of shared write bits without widening private permissions.
func TestWriteExceptionMigrationSafeMode(t *testing.T) {
	cases := []struct{ mode, want os.FileMode }{
		{0o666, 0o644}, {0o660, 0o640}, {0o644, 0o644}, {0o600, 0o600},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(fmt.Sprintf("%04o", tc.mode), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".standards.yaml")
			if err := os.WriteFile(path, []byte("version: 0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			assertExceptionMigrationWrite(t, path, tc.want)
		})
	}
}

// assertExceptionMigrationWrite checks migration readback and POSIX permission preservation.
func assertExceptionMigrationWrite(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := writeExceptionMigration(ctx, path, []byte("version: 1\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "version: 1\n" {
		t.Fatalf("migration readback: %q, %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != want {
		t.Fatalf("migration mode: %04o, want %04o", info.Mode().Perm(), want)
	}
}

// TestWriteExceptionMigrationCanceled pins that cancellation prevents replacement of the original manifest.
func TestWriteExceptionMigrationCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".standards.yaml")
	if err := os.WriteFile(path, []byte("version: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := writeExceptionMigration(ctx, path, []byte("version: 1\n")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration: %v, want context.Canceled", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "version: 0\n" {
		t.Fatalf("canceled migration changed manifest: %q, %v", data, err)
	}
}

// TestExceptionMigrationTerminalControls pins stripping of terminal control characters from migration output.
func TestExceptionMigrationTerminalControls(t *testing.T) {
	for _, value := range []string{"plain", "café", "", "\x00\x08\x09\x1b[2J\x7f\u009b2J\u202e\r\n\xff"} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			proposal := config.ExceptionMigration{
				Entry:  config.Exception{Rule: value, Path: value, Expires: value, Category: value, Entered: value},
				Source: value, Unresolved: value,
			}
			out, err := captureStdout(t, func() error {
				if got := printExceptionMigrations(value, []config.ExceptionMigration{proposal}); (got > 0) != (value != "") {
					t.Errorf("unresolved=%d", got)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			line := strings.TrimSuffix(out, "\n")
			if strings.ContainsFunc(line, func(r rune) bool { return !unicode.IsGraphic(r) }) {
				t.Fatalf("terminal controls retained: %q", out)
			}
			if strings.Count(out, "\n") != 1 || !strings.Contains(out, "status=") {
				t.Fatalf("migration status lost: %q", out)
			}
			mustContain(t, out, "path=", "rule=", "target=", "expires=", "category=", "entered=", "source=")
		})
	}
}

// TestExceptionsMigrateDryRunAndWrite pins dry-run output and file updates for exception migrations.
func TestExceptionsMigrateDryRunAndWrite(t *testing.T) {
	today := config.ExceptionDay(time.Now())
	expires := today.AddDate(0, 0, 91).Format(config.ExceptionDateLayout)
	path := writeExceptionsManifest(t,
		config.Exception{Rule: "HISS-11", Path: ".github/workflows/release.yml", Reason: "category D, builder migration", Expires: expires},
		config.Exception{Rule: "declared-linters", Tool: "gitleaks", Reason: "CI migration", Expires: expires},
		config.Exception{Rule: "declared-linters", Tool: "semgrep", Reason: "CI migration", Expires: expires})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range []bool{false, true} {
		args := []string{"migrate", "--config=" + path}
		if write {
			args = append(args, "--write")
		}
		out, err := captureStdout(t, func() error { return runExceptions(args) })
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, out, "release.yml", "gitleaks", "semgrep", "category=D", "entered="+today.Format(config.ExceptionDateLayout), "source=today")
		if strings.Count(strings.TrimSpace(out), "\n") != 2 {
			t.Fatalf("want one line per entry: %s", out)
		}
		if write {
			if _, err := config.LoadManifest(path); err != nil {
				t.Fatal(err)
			}
		} else if after, err := os.ReadFile(path); err != nil || string(after) != string(before) {
			t.Fatalf("dry run changed manifest: %v", err)
		}
	}
}

// TestExceptionsMigrateUnresolved pins error reporting when an exception proposal cannot be resolved.
func TestExceptionsMigrateUnresolved(t *testing.T) {
	today := config.ExceptionDay(time.Now())
	path := writeExceptionsManifest(t,
		config.Exception{Rule: "declared-linters", Tool: "semgrep", Reason: "migration", Expires: today.AddDate(0, 0, 91).Format(config.ExceptionDateLayout)},
		config.Exception{Rule: "declared-linters", Tool: "gitleaks", Reason: "too long", Expires: today.AddDate(0, 0, 121).Format(config.ExceptionDateLayout)})
	out, err := captureStdout(t, func() error { return runExceptions([]string{"migrate", "--write", "--config=" + path}) })
	if err == nil || !strings.Contains(err.Error(), "unresolved") {
		t.Fatalf("unresolved entry accepted: %s %v", out, err)
	}
	mustContain(t, out, "target=gitleaks", "status=unresolved", "no category admits")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Exceptions[0].Category != "D" || manifest.Exceptions[1].Category != "" || manifest.Exceptions[1].Entered != "" {
		t.Fatalf("wrong entries migrated: %+v", manifest.Exceptions)
	}
}

// TestExceptionsMigrateOneLineAndFlags pins single-line rendering of multiline entries and flag validation.
func TestExceptionsMigrateOneLineAndFlags(t *testing.T) {
	path := writeExceptionsManifest(t, config.Exception{Rule: "declared-linters", Tool: "semi\ngrep", Reason: "migration", Expires: "bad\ndate"})
	out, err := captureStdout(t, func() error { return runExceptions([]string{"migrate", "--config=" + path}) })
	if err == nil || strings.Count(out, "\n") != 1 || !strings.Contains(out, `target=semi\ngrep`) {
		t.Fatalf("expected one unresolved line, got %q %v", out, err)
	}
	for _, args := range [][]string{{"migrate", "--today=invalid"}, {"list", "--write"}} {
		if err := runExceptions(args); err == nil {
			t.Fatalf("accepted flags: %v", args)
		}
	}
}
