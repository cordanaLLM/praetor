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
	// Pre-push with malformed stdin exits 2.
	if _, code := runDocstringsWithStdin(t, "malformed-stdin-line\n", "check", "--path="+dir, "--pre-push"); code != 2 {
		t.Fatalf("expected exit 2 on malformed pre-push stdin, got %d", code)
	}
}

// runDocstringsGitOut executes git in dir and returns trimmed stdout.
func runDocstringsGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	envCtx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(envCtx, 30*time.Second)
	defer cancel()
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

// runDocstringsWithStdin runs runDocstrings with the given stdin text and captures stdout and exit code.
func runDocstringsWithStdin(t *testing.T, stdinText string, args ...string) (string, int) {
	t.Helper()
	origStdin := os.Stdin
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inW.WriteString(stdinText); err != nil {
		t.Fatal(err)
	}
	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdin = inR
	out, code := runDocstringsCaptured(t, args...)
	os.Stdin = origStdin
	if err := inR.Close(); err != nil {
		t.Fatal(err)
	}
	return out, code
}

// TestRunDocstrings_PrePush verifies pre-push stdin evaluation over pushed ranges, new branches,
// deletes, and unresolvable bases.
func TestRunDocstrings_PrePush(t *testing.T) {
	baseGo := "package pkg\n\nfunc LegacyUndocumented() {\n\t_ = 1\n}\n"
	dir := initDocstringsFixtureRepo(t, map[string]string{"pkg/work.go": baseGo})
	baseOID := runDocstringsGitOut(t, dir, "rev-parse", "HEAD")
	zeroOID := strings.Repeat("0", 40)

	// Commit a documented function, then dirty the worktree with an undocumented one.
	writeDocstringsFixtureFile(t, dir, "pkg/work.go", baseGo+"\n// Covered performs work.\nfunc Covered() {\n\t_ = 2\n}\n")
	runDocstringsGit(t, dir, "add", "pkg/work.go")
	runDocstringsGit(t, dir, "commit", "-q", "-m", "add covered")
	headOID := runDocstringsGitOut(t, dir, "rev-parse", "HEAD")
	writeDocstringsFixtureFile(t, dir, "pkg/work.go", baseGo+"\n// Covered performs work.\nfunc Covered() {\n\t_ = 2\n}\n\nfunc DirtyWorktree() {\n\t_ = 3\n}\n")

	// 1. Pushed range evaluated (dirty worktree edit does not count).
	line := "refs/heads/feat " + headOID + " refs/heads/feat " + baseOID + "\n"
	if out, code := runDocstringsWithStdin(t, line, "check", "--path="+dir, "--pre-push"); code != 0 {
		t.Fatalf("pushed range with dirty worktree: want exit 0, got %d:\n%s", code, out)
	}
	// 2. New branch (remote oid all zeros) uses merge base with origin/<default-branch>.
	newBranchLine := "refs/heads/feat " + headOID + " refs/heads/feat " + zeroOID + "\n"
	if out, code := runDocstringsWithStdin(t, newBranchLine, "check", "--path="+dir, "--pre-push"); code != 0 {
		t.Fatalf("new branch merge base: want exit 0, got %d:\n%s", code, out)
	}
	// 3. Delete (local oid all zeros) is skipped.
	deleteLine := "refs/heads/feat " + zeroOID + " refs/heads/feat " + headOID + "\n"
	if out, code := runDocstringsWithStdin(t, deleteLine, "check", "--path="+dir, "--pre-push"); code != 0 {
		t.Fatalf("delete update: want exit 0, got %d:\n%s", code, out)
	}
	// 4. Unresolvable base skips with message and exits 0 (never exit 2).
	runDocstringsGit(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	missingRemoteOID := strings.Repeat("f", 40)
	unresolvableLine := "refs/heads/feat " + headOID + " refs/heads/feat " + missingRemoteOID + "\n"
	out, code := runDocstringsWithStdin(t, unresolvableLine, "check", "--path="+dir, "--pre-push")
	if code != 0 || !strings.Contains(out, "skipped") || !strings.Contains(out, "CI checks") {
		t.Fatalf("unresolvable base: want exit 0 and skip message, got %d:\n%s", code, out)
	}
	// 5. Empty stdin (another use_stdin job read it) is a stated skip, not a silent pass.
	out, code = runDocstringsWithStdin(t, "", "check", "--path="+dir, "--pre-push")
	if code != 0 || !strings.Contains(out, "no pushed ref update on stdin") {
		t.Fatalf("empty stdin: want exit 0 and the stated skip, got %d:\n%s", code, out)
	}
	// 6. Expired pre-push stdin context exits 2.
	shortCtx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if err := runDocstringsPrePush(shortCtx, dir, nil, strings.NewReader(line)); commandExitCode(io.Discard, err) != 2 {
		t.Fatalf("expired pre-push stdin context: want exit 2, got err=%v", err)
	}
}

// TestRunDocstrings_Modes verifies CLI behavior and exit codes for warning, error, and off modes.
func TestRunDocstrings_Modes(t *testing.T) {
	dir := initDocstringsFixtureRepo(t, map[string]string{"pkg/work.go": "package pkg\n"})
	writeDocstringsFixtureFile(t, dir, "pkg/work.go", "package pkg\n\nfunc Undoc() {\n\t_ = 1\n}\n")

	writeDocstringsFixtureFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: warning\n      threshold: 80\n")
	outWarn, codeWarn := runDocstringsCaptured(t, "check", "--path="+dir)
	if codeWarn != 0 || !strings.Contains(outWarn, "warning") || !strings.Contains(outWarn, "Undoc") {
		t.Fatalf("warning mode: want exit 0 with warning and Undoc, got %d:\n%s", codeWarn, outWarn)
	}

	writeDocstringsFixtureFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: error\n      threshold: 80\n")
	if _, codeErr := runDocstringsCaptured(t, "check", "--path="+dir); codeErr != 1 {
		t.Fatalf("error mode: want exit 1, got %d", codeErr)
	}

	writeDocstringsFixtureFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: off\n      threshold: 80\n")
	if _, codeOff := runDocstringsCaptured(t, "check", "--path="+dir); codeOff != 0 {
		t.Fatalf("off mode: want exit 0, got %d", codeOff)
	}
}

// TestRunDocstrings_ModeOffWithoutOriginMain verifies that mode off exits 0 without origin/main.
func TestRunDocstrings_ModeOffWithoutOriginMain(t *testing.T) {
	dir := initDocstringsFixtureRepo(t, map[string]string{"pkg/work.go": "package pkg\n"})
	runDocstringsGit(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	writeDocstringsFixtureFile(t, dir, ".coderabbit.yaml", "reviews:\n  pre_merge_checks:\n    docstrings:\n      mode: off\n      threshold: 80\n")
	out, code := runDocstringsCaptured(t, "check", "--path="+dir)
	if code != 0 {
		t.Fatalf("expected exit 0 for mode off without origin/main, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(mode: off)") {
		t.Fatalf("expected output to mention mode off, got:\n%s", out)
	}
}
