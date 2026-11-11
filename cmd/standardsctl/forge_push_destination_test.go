// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestForgePushUniqueRanges checks that overlapping histories are bounded and analyzed once.
func TestForgePushUniqueRanges(t *testing.T) {
	dir := importCommitRange(t, maxAnalyzedCommits/2+1)
	env := testsupport.HermeticGitEnv(t)
	head := pushOID(t, dir, env, "refs/heads/range")
	base := pushOID(t, dir, env, "HEAD")
	pushGit(t, dir, env, "remote", "add", "origin", "https://example.invalid/origin")
	pushGit(t, dir, env, "update-ref", "refs/remotes/origin/main", base)
	zero := strings.Repeat("0", len(head))
	line := fmt.Sprintf("refs/heads/a %s refs/heads/a %s\nrefs/heads/b %s refs/heads/b %s\n", head, zero, head, zero)
	if out, err := pushInput(t, dir, line); err != nil {
		t.Fatalf("duplicate ranges: %v %s", err, out)
	}
	pushGit(t, dir, env, "checkout", "-q", "range")
	pushGit(t, dir, env, "commit", "--amend", "--allow-empty", "-qm", "invalid message")
	head = pushOID(t, dir, env, "HEAD")
	line = fmt.Sprintf("refs/heads/a %s refs/heads/a %s\nrefs/heads/b %s refs/heads/b %s\n", head, zero, head, zero)
	_, err := pushInput(t, dir, line)
	if err == nil || !strings.Contains(err.Error(), "1 of 501") {
		t.Fatalf("expected one bad commit: %v", err)
	}
}

// TestForgePushDestination scopes new-ref history to the named remote.
func TestForgePushDestination(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	pushGit(t, dir, env, "commit", "--amend", "-qm", "feat: initial")
	base := pushOID(t, dir, env, "HEAD")
	pushGit(t, dir, env, "remote", "add", "origin", "https://example.invalid/origin")
	pushGit(t, dir, env, "remote", "add", "fork", "https://example.invalid/fork")
	pushGit(t, dir, env, "update-ref", "refs/remotes/origin/main", base)
	writeFixtureFile(t, dir, "bad", "bad")
	gitCommitAll(t, dir, env, "invalid message")
	head := pushOID(t, dir, env, "HEAD")
	pushGit(t, dir, env, "update-ref", "refs/remotes/fork/x", head)
	line := fmt.Sprintf("refs/heads/new %s refs/heads/new %s\n", head, strings.Repeat("0", len(head)))
	for _, tc := range []struct {
		remote string
		fail   bool
	}{{"origin", true}, {"fork", false}, {"unknown", true}, {"f*", true}} {
		_, err := pushInput(t, dir, line, "--remote="+tc.remote)
		if (err != nil) != tc.fail {
			t.Errorf("remote %q: %v", tc.remote, err)
		}
	}
	if _, err := pushInput(t, dir, line, []string{}...); err == nil || !strings.Contains(err.Error(), "--remote") {
		t.Fatalf("missing remote: %v", err)
	}
}

// TestForgePushUnknownRemoteLimit explains how to avoid an unbounded unnamed push.
func TestForgePushUnknownRemoteLimit(t *testing.T) {
	dir := importCommitRange(t, maxAnalyzedCommits+1)
	env := testsupport.HermeticGitEnv(t)
	head := pushOID(t, dir, env, "refs/heads/range")
	line := fmt.Sprintf("refs/heads/new %s refs/heads/new %s\n", head, strings.Repeat("0", len(head)))
	_, err := pushInput(t, dir, line, "--remote=https://example.invalid/repo")
	if err == nil || !strings.Contains(err.Error(), "https://example.invalid/repo") || !strings.Contains(err.Error(), "named remote") {
		t.Fatalf("missing bounded-push guidance: %v", err)
	}
}
