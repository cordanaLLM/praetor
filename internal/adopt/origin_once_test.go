// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// originReads counts the git config reads of the origin remote git traced to file.
func originReads(t *testing.T, file string) int {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(data), "config --get remote.origin.url")
}

// One adoption reads the origin remote from several steps; it asks git once (util.WithOriginCache,
// #1382). Each adoption is its own session, so a second one asks again, once.
func TestAdopt_Boundary_ReadsTheOriginRemoteOncePerAdoption(t *testing.T) {
	repo := newTestRepo(t, "origin-once")
	trace := filepath.Join(t.TempDir(), "git.trace")
	t.Setenv("GIT_TRACE", trace)
	opts := AdoptOptions{Path: repo, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if got := originReads(t, trace); got != 1 {
		t.Errorf("one adoption read the origin remote %d times, want 1", got)
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if got := originReads(t, trace); got != 2 {
		t.Errorf("two adoptions read the origin remote %d times, want 2", got)
	}
}
