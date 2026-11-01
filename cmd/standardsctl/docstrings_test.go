// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// initDocstringsFixtureRepo creates a hermetic Git repository with origin/main set to the initial commit.
func initDocstringsFixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "praetor-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "praetor-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	dir := t.TempDir()
	for rel, content := range files {
		writeDocstringsFixtureFile(t, dir, rel, content)
	}
	runDocstringsGit(t, dir, "init", "-q", "-b", "main")
	runDocstringsGit(t, dir, "add", ".")
	runDocstringsGit(t, dir, "commit", "-q", "-m", "initial")
	runDocstringsGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

// writeDocstringsFixtureFile writes content to dir/rel.
func writeDocstringsFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runDocstringsGit executes git in dir under a hermetic environment.
func runDocstringsGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(envCtx, 30*time.Second)
	defer cancel()
	if out, err := util.RunGit(ctx, dir, args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// runDocstringsCaptured runs runDocstrings and captures stdout and the process exit code.
func runDocstringsCaptured(t *testing.T, args ...string) (string, int) {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	cmdErr := runDocstrings(args)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if cmdErr == nil {
		return buf.String(), 0
	}
	return buf.String(), commandExitCode(io.Discard, cmdErr)
}

// TestRunDocstrings_Positive_PassAndFailExitCodes verifies CLI output and exit codes 0 and 1 on a planted repo.
func TestRunDocstrings_Positive_PassAndFailExitCodes(t *testing.T) {
	baseGo := "package pkg\n\nfunc LegacyUndocumented() {\n\t_ = 1\n}\n"
	dir := initDocstringsFixtureRepo(t, map[string]string{"pkg/work.go": baseGo})

	// Planted: touched function without doc comment fails with exit 1; untouched undocumented function not counted.
	writeDocstringsFixtureFile(t, dir, "pkg/work.go", baseGo+"\nfunc NewWork() {\n\t_ = 2\n}\n")
	out, code := runDocstringsCaptured(t, "check", "--path="+dir, "--base=origin/main")
	if code != 1 {
		t.Fatalf("expected exit 1 on undocumented touched function, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "0.00% (0/1 covered, counted: 1, covered: 0, threshold: 80%)") ||
		!strings.Contains(out, "pkg/work.go:7 NewWork") {
		t.Fatalf("unexpected CLI output for failing check:\n%s", out)
	}

	// Same function with doc comment passes with exit 0.
	writeDocstringsFixtureFile(t, dir, "pkg/work.go", baseGo+"\n// NewWork performs the work.\nfunc NewWork() {\n\t_ = 2\n}\n")
	out, code = runDocstringsCaptured(t, "check", "--path="+dir, "--base=origin/main")
	if code != 0 {
		t.Fatalf("expected exit 0 on documented touched function, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "100.00% (1/1 covered, counted: 1, covered: 1, threshold: 80%)") {
		t.Fatalf("unexpected CLI output for passing check:\n%s", out)
	}
}

// TestRunDocstrings_NegativeAndBoundary verifies exit 2 on read/flag failures and exact threshold boundary.
func TestRunDocstrings_NegativeAndBoundary(t *testing.T) {
	dir := initDocstringsFixtureRepo(t, map[string]string{"pkg/work.go": "package pkg\n"})

	// Read failure (invalid base ref) exits 2.
	if _, code := runDocstringsCaptured(t, "check", "--path="+dir, "--base=no-such-ref"); code != 2 {
		t.Fatalf("expected exit 2 on invalid base ref, got %d", code)
	}
	// Unknown subcommand exits 2.
	if _, code := runDocstringsCaptured(t, "bogus"); code != 2 {
		t.Fatalf("expected exit 2 on unknown subcommand, got %d", code)
	}
	// Help exits 0.
	if _, code := runDocstringsCaptured(t, "--help"); code != 0 {
		t.Fatalf("expected exit 0 on --help, got %d", code)
	}
}
