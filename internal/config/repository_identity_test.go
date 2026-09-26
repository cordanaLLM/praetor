// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// identityRepo creates <tmp>/acme/<name>, a git repository with the given origin remote
// ("" adds none) and .standards.yaml body ("" writes none). The parent directory is named
// like an owner so a resolver that guessed from the path would find one.
func identityRepo(t *testing.T, name, remote, manifest string) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "acme", name)
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if remote != "" {
		if _, err := util.RunGit(t.Context(), repo, "remote", "add", "origin", remote); err != nil {
			t.Fatal(err)
		}
	}
	if manifest != "" {
		writePolicyFile(t, repo, ".standards.yaml", manifest)
	}
	return repo
}

func TestResolveRepositoryIdentity_Positive(t *testing.T) {
	manifest := "repository:\n  owner: acme\n  name: kit\n"
	cases := []struct {
		name, remote, manifest, explicit, fallback, wantOwner, wantName string
	}{
		{"manifest wins over remote", "https://github.com/other/app.git", manifest, "", "", "acme", "kit"},
		{"flag wins over manifest", "", manifest, "acme-labs", "", "acme-labs", "kit"},
		{"remote when no manifest", "https://github.com/acme/app.git", "", "", "fallback", "acme", "app"},
		{"remote fills a missing name", "git@github.com:other/app.git", "repository:\n  owner: acme\n", "", "", "acme", "app"},
		{"default owner last", "", "repository:\n  name: kit\n", "", "acme", "acme", "kit"},
	}
	for _, tc := range cases {
		repo := identityRepo(t, "checkout", tc.remote, tc.manifest)
		owner, name, err := ResolveRepositoryIdentity(t.Context(), repo, tc.explicit, tc.fallback)
		if err != nil || owner != tc.wantOwner || name != tc.wantName {
			t.Errorf("%s: %s/%s, %v; want %s/%s", tc.name, owner, name, err, tc.wantOwner, tc.wantName)
		}
	}
}

func TestResolveRepositoryIdentity_Negative(t *testing.T) {
	// The parent directory is "acme", yet without a manifest or a network remote the owner is
	// unknown: the checkout path is never read.
	bare := identityRepo(t, "app", "", "")
	if _, _, err := ResolveRepositoryIdentity(t.Context(), bare, "", ""); !errors.Is(err, ErrOwnerUnknown) {
		t.Fatalf("no source: %v, want ErrOwnerUnknown", err)
	}
	if !strings.Contains(ErrOwnerUnknown.Error(), "pass --owner, set repository.owner, or set forge.default_owner") {
		t.Fatalf("owner error text drifted from ADR-0014: %v", ErrOwnerUnknown)
	}
	// A local-path remote says where a copy sits, not which forge repository it is.
	local := identityRepo(t, "app", filepath.Join(t.TempDir(), "acme", "app.git"), "repository:\n  owner: acme\n")
	if _, _, err := ResolveRepositoryIdentity(t.Context(), local, "", ""); !errors.Is(err, ErrRepositoryNameUnknown) {
		t.Fatalf("local remote: %v, want ErrRepositoryNameUnknown", err)
	}
	if _, _, err := ResolveRepositoryIdentity(t.Context(), filepath.Join(t.TempDir(), "missing"), "acme", ""); err == nil {
		t.Fatal("a missing directory resolved")
	}
	// An invalid manifest is reported, not skipped in favour of the remote.
	broken := identityRepo(t, "app", "https://github.com/acme/app.git", "repository: [\n")
	if _, _, err := ResolveRepositoryIdentity(t.Context(), broken, "", ""); err == nil || errors.Is(err, ErrOwnerUnknown) {
		t.Fatalf("invalid manifest: %v, want a parse error", err)
	}
	// A remote read that did not complete is an error, never "no identity".
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	withRemote := identityRepo(t, "app", "https://github.com/acme/app.git", "")
	if _, _, err := ResolveRepositoryIdentity(cancelled, withRemote, "", "acme"); err == nil || errors.Is(err, ErrOwnerUnknown) {
		t.Fatalf("cancelled remote read: %v, want the context error", err)
	}
	var nilContext context.Context
	if _, _, err := ResolveRepositoryIdentity(nilContext, withRemote, "", ""); err == nil {
		t.Fatal("a nil context resolved")
	}
}

func TestResolveRepositoryIdentity_Boundary(t *testing.T) {
	longOwner := strings.Repeat("a", 39)
	repo := identityRepo(t, "kit", "", "repository:\n  name: kit\n")
	if owner, _, err := ResolveRepositoryIdentity(t.Context(), repo, "", longOwner); err != nil || owner != longOwner {
		t.Fatalf("39-character owner: %q, %v", owner, err)
	}
	for _, owner := range []string{strings.Repeat("a", 40), "ac me", "-acme"} {
		if _, _, err := ResolveRepositoryIdentity(t.Context(), repo, owner, ""); err == nil {
			t.Errorf("invalid owner %q accepted", owner)
		}
	}
}
