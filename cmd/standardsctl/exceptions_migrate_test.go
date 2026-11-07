// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
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
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestWriteExceptionMigrationSourceChanges pins concurrent-edit refusal and exact-byte snapshot acceptance.
func TestWriteExceptionMigrationSourceChanges(t *testing.T) {
	for _, name := range []string{"unchanged", "changed", "grown", "shorter", "restored"} {
		t.Run(name, func(t *testing.T) {
			path, plan := plannedExceptionMigration(t)
			current := rewriteExceptionMigrationSource(t, path, plan.Source, name)
			want := plan.Data
			err := writeExceptionMigration(t.Context(), path, plan.Data, plan.Source)
			if name != "unchanged" && name != "restored" {
				want = current
				if !errors.Is(err, errExceptionMigrationManifestChanged) || err.Error() != "manifest changed since it was planned; rerun the migration" {
					t.Errorf("changed manifest: got %v, want source-change error", err)
				}
			} else if err != nil {
				t.Errorf("identical source refused: %v", err)
			}
			if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
				t.Fatalf("manifest after write: %q, want %q, err=%v", got, want, err)
			}
		})
	}
}

// plannedExceptionMigration builds a legacy manifest and verifies its migration retains the exact parsed snapshot.
func plannedExceptionMigration(t *testing.T) (string, config.ExceptionMigrationPlan) {
	t.Helper()
	today := config.ExceptionDay(time.Now())
	path := writeExceptionsManifest(t, config.Exception{Rule: "declared-linters", Tool: "semgrep",
		Reason: "migration", Expires: today.AddDate(0, 0, 91).Format(config.ExceptionDateLayout)})
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := config.PlanExceptionMigrationSnapshot(t.Context(), path, today)
	if err != nil || len(plan.Proposals) != 1 || plan.Proposals[0].Unresolved != "" {
		t.Fatalf("plan: proposals=%+v err=%v", plan.Proposals, err)
	}
	if !bytes.Equal(plan.Source, source) {
		t.Fatal("plan did not retain exact source bytes")
	}
	return path, plan
}

// rewriteExceptionMigrationSource simulates an edit, optionally restoring the original bytes before applying the plan.
func rewriteExceptionMigrationSource(t *testing.T, path string, source []byte, name string) []byte {
	t.Helper()
	if name == "unchanged" {
		return source
	}
	edited := bytes.Replace(source, []byte("migration"), []byte("different"), 1)
	switch name {
	case "grown":
		edited = append(edited, []byte("# concurrent edit\n")...)
	case "shorter":
		edited = edited[:len(edited)-1]
	}
	if bytes.Equal(source, edited) {
		t.Fatal("fixture edit did not change source")
	}
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if name == "restored" {
		if err := os.WriteFile(path, source, 0o600); err != nil {
			t.Fatal(err)
		}
		return source
	}
	return edited
}

// TestWriteExceptionMigrationRefusesSymlink pins refusal without changing the link or its in-directory target.
func TestWriteExceptionMigrationRefusesSymlink(t *testing.T) {
	ctx := testsupport.DeadlineContext(t)
	root := t.TempDir()
	const original = "version: 1\n"
	target := filepath.Join(root, "target.yaml")
	if err := util.WriteFileConfined(root, filepath.Base(target), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".standards.yaml")
	symlinkOrSkip(t, filepath.Base(target), path)
	if err := writeExceptionMigration(ctx, path, []byte("version: 1\nexceptions: []\n"), []byte(original)); err == nil {
		t.Error("symlinked manifest accepted")
	}
	if data, err := util.ReadConfinedLimited(root, filepath.Base(target), 1024); err != nil || string(data) != original {
		t.Errorf("symlink target changed: %q, %v", data, err)
	}
	if link, err := os.Readlink(path); err != nil || link != filepath.Base(target) {
		t.Errorf("manifest link changed: %q, %v", link, err)
	}
}

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
	if err := writeExceptionMigration(ctx, path, []byte("version: 1\n"), []byte("version: 0\n")); err != nil {
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
	if err := writeExceptionMigration(ctx, path, []byte("version: 1\n"), []byte("version: 0\n")); !errors.Is(err, context.Canceled) {
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
