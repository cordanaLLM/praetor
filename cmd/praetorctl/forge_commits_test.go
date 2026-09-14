package main

import (
	"context"
	"fmt"
	"os/exec"
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
