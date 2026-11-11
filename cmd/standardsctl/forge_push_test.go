// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

func pushFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	pushGit(t, dir, env, "checkout", "-b", "feature")
	writeFixtureFile(t, dir, "change.txt", "change\n")
	gitCommitAll(t, dir, env, "feat: add x")
	return dir, env
}

func pushGit(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	if out, err := runFixtureGit(t, dir, env, args...); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func TestForgePrePush_Positive_UpstreamAndFirstPush(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-push", true: "upstream"}[upstream], func(t *testing.T) {
			dir, env := pushFixture(t)
			pushGit(t, dir, env, "remote", "add", "origin", "https://example.invalid/repo.git")
			pushGit(t, dir, env, "update-ref", "refs/remotes/origin/main", "main")
			if upstream {
				pushGit(t, dir, env, "update-ref", "refs/remotes/origin/feature", "main")
				pushGit(t, dir, env, "branch", "--set-upstream-to=origin/feature")
			}
			out, err := captureStdout(t, func() error {
				return runForge([]string{"check-commits", "--pre-push", "--repo=" + dir})
			})
			if err != nil || !strings.Contains(out, "1 commit(s)") {
				t.Fatalf("range resolution: %v\n%s", err, out)
			}
			writeFixtureFile(t, dir, "change.txt", "bad change\n")
			gitCommitAll(t, dir, env, "feat!: drop x")
			if err := runForge([]string{"check-commits", "--pre-push", "--repo=" + dir}); err == nil || !strings.Contains(err.Error(), "HISS-14") {
				t.Fatalf("breaking commit accepted: %v", err)
			}
		})
	}
}

func TestForgePrePush_Negative_DetachedMissingAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      []string
	}{
		{"detached", "attached HEAD", []string{"checkout", "--detach"}},
		{"missing default", "resolve pre-push merge base", []string{"branch", "-D", "main"}},
		{"empty", "no commits checked", []string{"branch", "-f", "main", "HEAD"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, env := pushFixture(t)
			pushGit(t, dir, env, tc.setup...)
			err := runForge([]string{"check-commits", "--pre-push", "--repo=" + dir})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestForgePrePush_Boundary_DeclaredDefaultAndExplicitRange(t *testing.T) {
	dir, env := pushFixture(t)
	pushGit(t, dir, env, "branch", "-m", "main", "trunk")
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nrepository:\n  default_branch: trunk\n")
	if err := runForge([]string{"check-commits", "--pre-push", "--repo=" + dir}); err != nil {
		t.Fatalf("declared local default: %v", err)
	}
	for _, flags := range [][]string{{"--base=HEAD~1"}, {"--head=HEAD~1"}} {
		args := append([]string{"check-commits", "--pre-push", "--repo=" + dir}, flags...)
		if err := runForge(args); err == nil || !strings.Contains(err.Error(), "no explicit --base") {
			t.Fatalf("conflicting mode accepted: %v", err)
		}
	}
	if err := runForge([]string{"check-commits", "--repo=" + dir, "--base=HEAD", "--head=HEAD"}); err != nil {
		t.Fatalf("explicit CI empty range changed: %v", err)
	}
}

func TestForgePrePush_Negative_MissingConfiguredUpstream(t *testing.T) {
	dir, env := pushFixture(t)
	pushGit(t, dir, env, "remote", "add", "origin", "https://example.invalid/repo.git")
	pushGit(t, dir, env, "update-ref", "refs/remotes/origin/feature", "main")
	pushGit(t, dir, env, "branch", "--set-upstream-to=origin/feature")
	pushGit(t, dir, env, "update-ref", "-d", "refs/remotes/origin/feature")
	err := runForge([]string{"check-commits", "--pre-push", "--repo=" + dir})
	if err == nil || !strings.Contains(err.Error(), "resolve pre-push merge base with refs/remotes/origin/feature") {
		t.Fatalf("missing configured upstream must not fall back to main: %v", err)
	}
}

func TestForgePrePush_Boundary_RecordedRemoteDefaultPreferred(t *testing.T) {
	dir, env := pushFixture(t)
	pushGit(t, dir, env, "branch", "-m", "main", "trunk")
	pushGit(t, dir, env, "remote", "add", "origin", "https://example.invalid/repo.git")
	pushGit(t, dir, env, "update-ref", "refs/remotes/origin/trunk", "trunk")
	pushGit(t, dir, env, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	pushGit(t, dir, env, "branch", "-f", "trunk", "HEAD")
	out, err := captureStdout(t, func() error {
		return runForge([]string{"check-commits", "--pre-push", "--repo=" + dir})
	})
	if err != nil || !strings.Contains(out, "1 commit(s)") {
		t.Fatalf("recorded remote default must take precedence over the advanced local branch: %v\n%s", err, out)
	}
}

func TestForgeCheckMessage_PlantedMigration(t *testing.T) {
	for _, tc := range []struct {
		message string
		valid   bool
	}{
		{"feat!: drop x\n", false},
		{"feat!: drop x\n\nMigration: use the replacement API\n", true},
		{"drop x\n", false},
	} {
		path := writeFixtureFile(t, t.TempDir(), "COMMIT_EDITMSG", tc.message)
		err := runForge([]string{"check-message", path})
		if (err == nil) != tc.valid {
			t.Errorf("message %q: valid=%v, error=%v", tc.message, tc.valid, err)
		}
	}
}
