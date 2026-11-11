// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func pushInput(t *testing.T, dir, input string) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err = f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	defer func() {
		os.Stdin = old
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	return captureStdout(t, func() error { return runForge([]string{"check-commits", "--pre-push", "--repo=" + dir}) })
}

func pushOID(t *testing.T, dir string, env []string, ref string) string {
	t.Helper()
	out, err := runFixtureGit(t, dir, env, "rev-parse", ref)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func TestForgePrePushRefs(t *testing.T) {
	dir := t.TempDir()
	env := initGitFixture(t, dir)
	pushGit(t, dir, env, "commit", "--amend", "-qm", "feat: initial")
	base := pushOID(t, dir, env, "HEAD")
	zero := strings.Repeat("0", len(base))
	line := func(ref, local, remote string) string { return fmt.Sprintf("%s %s %s %s\n", ref, local, ref, remote) }
	for _, tc := range []struct{ name, input string }{
		{"first", line("refs/heads/main", base, zero)},
		{"tag", line("refs/tags/v1", base, zero)},
		{"delete", line("(delete)", zero, base)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := pushInput(t, dir, tc.input); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		})
	}
	pushGit(t, dir, env, "checkout", "-qb", "release")
	writeFixtureFile(t, dir, "release", "bad")
	gitCommitAll(t, dir, env, "invalid release")
	release := pushOID(t, dir, env, "HEAD")
	pushGit(t, dir, env, "checkout", "-qb", "feature", "main")
	writeFixtureFile(t, dir, "feature", "good")
	gitCommitAll(t, dir, env, "feat: feature")
	t.Run("non-head-bad", func(t *testing.T) {
		if _, err := pushInput(t, dir, line("refs/heads/release", release, base)); err == nil {
			t.Fatal("bad release accepted")
		}
	})
	t.Run("bad-update", func(t *testing.T) {
		if _, err := pushInput(t, dir, line("refs/heads/feature", release, base)); err == nil {
			t.Fatal("bad update accepted")
		}
	})
	pushGit(t, dir, env, "checkout", "-q", "release")
	writeFixtureFile(t, dir, "release", "good")
	gitCommitAll(t, dir, env, "feat: good")
	good := pushOID(t, dir, env, "HEAD")
	pushGit(t, dir, env, "checkout", "-q", "feature")
	writeFixtureFile(t, dir, "feature", "bad")
	gitCommitAll(t, dir, env, "invalid feature")
	t.Run("non-head-good", func(t *testing.T) {
		if out, err := pushInput(t, dir, line("refs/heads/release", good, release)); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		if _, err := pushInput(t, dir, "bad input\n"); err == nil || !strings.Contains(err.Error(), "bad input") {
			t.Fatalf("malformed input: %v", err)
		}
	})
	t.Run("line-bound", func(t *testing.T) {
		if _, err := pushInput(t, dir, strings.Repeat(line("refs/tags/v1", base, zero), 4097)); err == nil || !strings.Contains(err.Error(), "4096") {
			t.Fatalf("line limit: %v", err)
		}
	})
}
