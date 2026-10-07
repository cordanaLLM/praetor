package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The HISS-14 commit gate is only real if the command exists and refuses bad input.
func TestForgeCheckCommits_Positive_EmptyRange(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	if err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD", "--head=HEAD"}); err != nil {
		t.Fatalf("expected an empty commit range to pass, got %v", err)
	}
}

func TestForgeCheckCommits_Negative_MissingBaseAndHostileRevision(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	if err := dispatchCommand("forge", []string{"check-commits"}); err == nil {
		t.Fatal("expected an error when --base is missing")
	}

	err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD;rm -rf /", "--head=HEAD"})
	if err == nil {
		t.Fatal("expected a shell metacharacter in a revision to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid git revision") {
		t.Fatalf("expected the revision validation error, got %v", err)
	}
}

func TestForgeCheckCommits_Boundary_UnknownRevision(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	err := dispatchCommand("forge", []string{
		"check-commits", "--repo=" + dir, "--base=0000000000000000000000000000000000000000", "--head=HEAD",
	})
	if err == nil {
		t.Fatal("expected an error for a revision that does not exist")
	}
}

func TestForgeCheckCommits_EnforcesBreakingMigrationFooter(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "change.txt", "with migration\n")
	gitCommitAll(t, dir, env, "feat!: change interface\n\nMigration: switch to the replacement API")
	args := []string{"check-commits", "--repo=" + dir, "--base=HEAD~1", "--head=HEAD"}
	if err := dispatchCommand("forge", args); err != nil {
		t.Fatalf("breaking commit with migration footer: %v", err)
	}
	writeFixtureFile(t, dir, "change.txt", "missing migration\n")
	gitCommitAll(t, dir, env, "feat!: change interface again")
	if err := dispatchCommand("forge", args); err == nil || !strings.Contains(err.Error(), "HISS-14 violation") {
		t.Fatalf("expected missing Migration footer failure, got %v", err)
	}
	if err := dispatchCommand("forge", append(args, "ignored")); err == nil {
		t.Fatal("unexpected positional argument must fail")
	}
}

// importCommitRange builds a real, bounded history in a temporary repository in one
// Git process. No developer repository, credentials or hooks are involved.
func importCommitRange(t *testing.T, count int) string {
	t.Helper()
	if count < 1 || count > maxAnalyzedCommits+1 {
		t.Fatal("fixture count outside the bounded test range")
	}
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	var input strings.Builder
	for i := 0; i < count; i++ {
		message := fmt.Sprintf("chore: range fixture %d", i)
		fragment := fmt.Sprintf("commit refs/heads/range\ncommitter Test <test@example.invalid> 1700000000 +0000\ndata %d\n%s\n", len(message), message)
		input.WriteString(fragment)
		if i == 0 {
			input.WriteString("from HEAD\n")
		}
		input.WriteString("\n")
	}
	input.WriteString("done\n")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "fast-import", "--quiet")
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(input.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("import commit fixture: %v (%s)", err, out)
	}
	return dir
}

func TestForgeCheckCommits_Boundary_CommitLimit(t *testing.T) {
	dir := importCommitRange(t, maxAnalyzedCommits+1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	messages, err := commitRange(ctx, dir, "HEAD", "refs/heads/range~1")
	if err != nil || len(messages) != maxAnalyzedCommits {
		t.Fatalf("exactly %d commits: got %d, %v", maxAnalyzedCommits, len(messages), err)
	}
	if _, err := commitRange(ctx, dir, "HEAD", "refs/heads/range"); err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("range overflow must fail instead of omitting old commits, got %v", err)
	}
}

// TestForgeCheckCommits_Negative_SubjectBreaksPolicy (#61): CI re-runs the commit message policy
// over every commit of a pull request, so a commit made without its commit-msg hook (this fixture
// installs none) and with a subject the hook refuses fails the range, named by its short SHA,
// while the range of the commit before it still passes.
func TestForgeCheckCommits_Negative_SubjectBreaksPolicy(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "change.txt", "good\n")
	gitCommitAll(t, dir, env, "fix(change): keep the policy")
	writeFixtureFile(t, dir, "change.txt", "bad\n")
	gitCommitAll(t, dir, env, "update change.txt")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD~2", "--head=HEAD"})
	})
	if err == nil || !strings.Contains(err.Error(), "commit message policy violation: 1 of 2 commit(s) fail") ||
		!strings.Contains(err.Error(), `Commit subject must be 'type(scope): description' (scope optional)`) {
		t.Fatalf("expected the bad subject to fail the range, got %v\n%s", err, out)
	}
	if !strings.Contains(out, `got "update change.txt"`) {
		t.Fatalf("the failure must quote the subject:\n%s", out)
	}
	if err := dispatchCommand("forge", []string{"check-commits", "--repo=" + dir, "--base=HEAD~2", "--head=HEAD~1"}); err != nil {
		t.Fatalf("the conventional commit alone: %v", err)
	}
}

// TestForgeCheckMessage_Positive_CleansTheHookMessage (#61): the commit-msg hook hands git's
// message file before cleanup; its comment lines and everything after the scissors line are not
// part of the message.
func TestForgeCheckMessage_Positive_CleansTheHookMessage(t *testing.T) {
	path := writeFixtureFile(t, t.TempDir(), "COMMIT_EDITMSG", "# Use type(scope): description\nfix(hooks): gate changes\n\n"+
		"Signed-off-by: Test <test@example.invalid>\n# Please enter the commit message\n"+
		"# ------------------------ >8 ------------------------\nfeat!: a diff line, not the message\n")
	if err := dispatchCommand("forge", []string{"check-message", path}); err != nil {
		t.Fatalf("clean conventional message: %v", err)
	}
}

// TestForgeCheckMessage_Negative_PolicyAndUsage (#61): a subject outside the policy and a
// breaking change without its Migration: footer fail; a missing file, no file and two files are
// refused.
func TestForgeCheckMessage_Negative_PolicyAndUsage(t *testing.T) {
	dir := t.TempDir()
	for message, want := range map[string]string{
		"bad subject\n":             "Commit subject must be 'type(scope): description' (scope optional)",
		"feat(api)!: drop a call\n": "HISS-14 violation",
	} {
		path := writeFixtureFile(t, dir, "COMMIT_EDITMSG", message)
		if err := dispatchCommand("forge", []string{"check-message", path}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: got %v, want %q", message, err, want)
		}
	}
	for _, args := range [][]string{{"check-message"}, {"check-message", "a", "b"}, {"check-message", filepath.Join(dir, "missing")}} {
		if err := dispatchCommand("forge", args); err == nil {
			t.Fatalf("%v must fail", args)
		}
	}
}

// TestForgeCheckMessage_Boundary_EmptyAndOversizedMessages (#61): a message of comments only is
// empty, a file past the read bound is refused rather than read in part, and one of exactly the
// bound is read.
func TestForgeCheckMessage_Boundary_EmptyAndOversizedMessages(t *testing.T) {
	dir := t.TempDir()
	comments := writeFixtureFile(t, dir, "comments", "# nothing but comments\n")
	if err := dispatchCommand("forge", []string{"check-message", comments}); err == nil || !strings.Contains(err.Error(), "empty commit message") {
		t.Fatalf("comments only: %v", err)
	}
	subject := "fix: at the bound\n"
	exact := writeFixtureFile(t, dir, "exact", subject+strings.Repeat("x", maxCommitMessageBytes-len(subject)))
	if err := dispatchCommand("forge", []string{"check-message", exact}); err != nil {
		t.Fatalf("a message of exactly the bound: %v", err)
	}
	oversized := writeFixtureFile(t, dir, "oversized", subject+strings.Repeat("x", maxCommitMessageBytes))
	if err := dispatchCommand("forge", []string{"check-message", oversized}); err == nil {
		t.Fatal("a message past the read bound must fail")
	}
}
