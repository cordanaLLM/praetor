// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

// originFixture returns a repository whose origin remote names acme/first, and a function that
// points the remote elsewhere through git itself.
func originFixture(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git required")
	}
	repo := t.TempDir()
	ctx, err := WithCommandEnvironment(t.Context(), hermeticOriginEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := RunGit(ctx, repo, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://github.com/acme/first.git")
	return repo, git
}

// hermeticOriginEnv keeps the operator's git configuration out of the fixture.
func hermeticOriginEnv(t *testing.T) []string {
	t.Helper()
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + t.TempDir() + "/none"}
}

// One adoption reads the origin remote from several steps; under WithOriginCache git is asked
// once per repository (#1382). Positive: after the remote is changed through git, a read under the
// same cached context still answers the first remote, so no second git config ran; a fresh context
// reads the new one. Negative: without the cache every read asks git. Boundary: "no origin remote"
// is git's answer and is held too, and a read git did not answer (a cancelled context) is not held.
func TestWithOriginCache_3D(t *testing.T) {
	repo, git := originFixture(t)
	cached := WithOriginCache(t.Context())
	if owner, name, err := ResolveRemoteIdentity(cached, repo); err != nil || owner != "acme" || name != "first" {
		t.Fatalf("first read: %s/%s, %v", owner, name, err)
	}
	git("remote", "set-url", "origin", "https://github.com/acme/second.git")
	if _, name, err := ResolveRemoteIdentity(cached, repo); err != nil || name != "first" {
		t.Errorf("second read under the cache asked git again: %s, %v", name, err)
	}
	if _, name, err := ResolveRemoteIdentity(t.Context(), repo); err != nil || name != "second" {
		t.Errorf("an uncached read: %s, %v; want the remote git now holds", name, err)
	}
	if WithOriginCache(cached) != cached {
		t.Error("a context that already caches got a second cache")
	}

	unset, git := originFixture(t)
	git("remote", "remove", "origin")
	none := WithOriginCache(t.Context())
	if _, err := ReadOriginRemote(none, unset); !errors.Is(err, ErrRepoIdentityUnresolved) {
		t.Fatalf("no origin remote: %v", err)
	}
	git("remote", "add", "origin", "https://github.com/acme/third.git")
	if _, err := ReadOriginRemote(none, unset); !errors.Is(err, ErrRepoIdentityUnresolved) {
		t.Errorf("git's answer of no origin remote is not held: %v", err)
	}

	stopped, cancel := context.WithCancel(WithOriginCache(t.Context()))
	cancel()
	if _, err := ReadOriginRemote(stopped, unset); err == nil || errors.Is(err, ErrRepoIdentityUnresolved) {
		t.Fatalf("a cancelled read: %v; want an unanswered read", err)
	}
	if _, name, err := ResolveRemoteIdentity(context.WithoutCancel(stopped), unset); err != nil || name != "third" {
		t.Errorf("an unanswered read was held: %s, %v", name, err)
	}
}
