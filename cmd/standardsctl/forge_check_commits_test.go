// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

func TestForgeCheckCommits_PlantedOffendingCommit(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "file.txt", "base\n")
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "file.txt", "change\n")
	gitCommitAll(t, dir, env, "feat: add feature\n\nGenerated with Claude Code")
	offendingSHA, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	shortSHA := buildid.Short(strings.TrimSpace(offendingSHA))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=HEAD~1", "--head=HEAD"})
	if err == nil {
		t.Fatal("expected runForgeCheckCommits to fail on offending commit, but succeeded")
	}
	if !strings.Contains(err.Error(), shortSHA) {
		t.Fatalf("expected error to name short SHA %s, got %v", shortSHA, err)
	}
	if !strings.Contains(err.Error(), "no tool attribution") {
		t.Fatalf("expected error to mention attribution policy, got %v", err)
	}
}

// TestForgeCheckMessage_PlantedOffendingMessage checks that the message policy rejects an intentionally invalid commit message.
func TestForgeCheckMessage_PlantedOffendingMessage(t *testing.T) {
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "COMMIT_EDITMSG")
	if err := os.WriteFile(msgFile, []byte("feat: add feature\n\nCo-authored-by: GitHub Copilot\n"), 0600); err != nil {
		t.Fatal(err)
	}

	err := runForgeCheckMessage([]string{msgFile})
	if err == nil {
		t.Fatal("expected runForgeCheckMessage to fail on attribution trailer, but succeeded")
	}
	if !strings.Contains(err.Error(), "no tool attribution") {
		t.Fatalf("expected error to mention attribution policy, got %v", err)
	}
}

func TestForgeCheckCommits_ExcludedCommitPassesAndUnexcludedFails(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "file.txt", "base\n")
	env := initGitFixture(t, dir)
	writeFixtureFile(t, dir, "file.txt", "bad\n")
	gitCommitAll(t, dir, env, "feat: bad commit\n\nGenerated with Claude Code")
	badSHA, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	badSHA = strings.TrimSpace(badSHA)
	writeFixtureFile(t, dir, "file.txt", "good\n")
	gitCommitAll(t, dir, env, "fix: good commit\n\nvalid body text")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=HEAD~2", "--head=HEAD"})
	if err == nil {
		t.Fatal("expected failure without exclude, but succeeded")
	}

	err = runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=HEAD~2", "--head=HEAD", "--exclude=" + badSHA})
	if err != nil {
		t.Fatalf("expected pass with exclude, got %v", err)
	}
}

func TestForgeCheckCommits_SecondParentMergePassesWithFirstParentAndFailsWithout(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "file.txt", "base\n")
	env := initGitFixture(t, dir)
	baseSHA, err := runFixtureGit(t, dir, env, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	baseSHA = strings.TrimSpace(baseSHA)

	if _, err := runFixtureGit(t, dir, env, "checkout", "-b", "second-parent"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	writeFixtureFile(t, dir, "file2.txt", "second parent\n")
	gitCommitAll(t, dir, env, "feat: bad message on second parent\n\nGenerated with Claude Code")

	if _, err := runFixtureGit(t, dir, env, "checkout", "main"); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := runFixtureGit(t, dir, env, "merge", "--no-ff", "-m", "Merge branch 'second-parent' into main", "second-parent"); err != nil {
		t.Fatalf("merge: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=" + baseSHA, "--head=HEAD"})
	if err == nil {
		t.Fatal("expected failure without --first-parent, but succeeded")
	}

	err = runForgeCheckCommits(ctx, []string{"--repo=" + dir, "--base=" + baseSHA, "--head=HEAD", "--first-parent"})
	if err != nil {
		t.Fatalf("expected pass with --first-parent, got %v", err)
	}
}

func TestForgeCheckCommits_InvalidExcludeRefused(t *testing.T) {
	dir := t.TempDir()
	initGitFixture(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := runForgeCheckCommits(ctx, []string{
		"--repo=" + dir, "--base=HEAD~1", "--head=HEAD", "--exclude=HEAD;rm -rf /",
	})
	if err == nil {
		t.Fatal("expected invalid exclude revision to be refused")
	}
	if !strings.Contains(err.Error(), "invalid git revision") {
		t.Fatalf("expected error mentioning invalid git revision, got %v", err)
	}
}

func TestForgeCheckCommits_BoundaryMaxAnalyzedCommitsRefuses(t *testing.T) {
	dir := importCommitRange(t, maxAnalyzedCommits+1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := runForgeCheckCommits(ctx, []string{
		"--repo=" + dir, "--base=HEAD", "--head=refs/heads/range",
	})
	if err == nil {
		t.Fatal("expected error when commit range exceeds maxAnalyzedCommits, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("expected error mentioning exceeds maximum, got %v", err)
	}
}
