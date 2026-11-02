// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

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
