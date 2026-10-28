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

func TestForgeCheckMessage_PlantedOffendingMessage(t *testing.T) {
	dir := t.TempDir()
	msgFile := filepath.Join(dir, "COMMIT_EDITMSG")
	content := "feat: add feature\n\nCo-authored-by: GitHub Copilot\n"
	if err := os.WriteFile(msgFile, []byte(content), 0600); err != nil {
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
