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

// ResolveRepositoryName names a repository the way ResolveRepositoryIdentity does, without an
// owner: the manifest's repository.name, then the origin remote, never the directory (#606).
func TestResolveRepositoryName_Positive(t *testing.T) {
	cases := []struct{ name, dir, remote, manifest, want string }{
		{"manifest wins over remote and directory", "renamed-checkout", "https://github.com/acme/other.git", "repository:\n  name: platform\n", "platform"},
		{"remote when the manifest names none", "renamed-checkout", "git@github.com:acme/platform.git", "repository:\n  owner: acme\n", "platform"},
		{"no owner anywhere is no error", "checkout", "", "repository:\n  name: platform\n", "platform"},
		{"manifest name despite a remote ending in /.", "checkout", "https://github.com/acme/.", "repository:\n  name: platform\n", "platform"},
	}
	for _, tc := range cases {
		repo := identityRepo(t, tc.dir, tc.remote, tc.manifest)
		if got, err := ResolveRepositoryName(t.Context(), repo); err != nil || got != tc.want {
			t.Errorf("%s: ResolveRepositoryName = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	named := identityRepo(t, "checkout", "https://github.com/acme/other.git", "repository:\n  name: platform\n")
	if got, err := ManifestRepositoryName(named); err != nil || got != "platform" {
		t.Errorf("ManifestRepositoryName = %q, %v; want platform", got, err)
	}
}

func TestResolveRepositoryName_Negative(t *testing.T) {
	// Neither source names the repository: the directory name is never read.
	bare := identityRepo(t, "app", "", "")
	if got, err := ResolveRepositoryName(t.Context(), bare); !errors.Is(err, ErrRepositoryNameUnknown) {
		t.Fatalf("no source: %q, %v; want ErrRepositoryNameUnknown", got, err)
	}
	// ManifestRepositoryName never reads the remote.
	remoteOnly := identityRepo(t, "app", "https://github.com/acme/app.git", "")
	if got, err := ManifestRepositoryName(remoteOnly); !errors.Is(err, ErrRepositoryNameUnknown) {
		t.Fatalf("remote only: ManifestRepositoryName = %q, %v; want ErrRepositoryNameUnknown", got, err)
	}
	// An invalid manifest and a remote read that did not complete are errors, not "unknown".
	broken := identityRepo(t, "app", "https://github.com/acme/app.git", "repository: [\n")
	for label, resolve := range map[string]func() (string, error){
		"resolve":  func() (string, error) { return ResolveRepositoryName(t.Context(), broken) },
		"manifest": func() (string, error) { return ManifestRepositoryName(broken) },
	} {
		if _, err := resolve(); err == nil || errors.Is(err, ErrRepositoryNameUnknown) {
			t.Errorf("%s: invalid manifest: %v, want a parse error", label, err)
		}
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveRepositoryName(cancelled, remoteOnly); err == nil || errors.Is(err, ErrRepositoryNameUnknown) {
		t.Fatalf("cancelled remote read: %v, want the context error", err)
	}
	var nilContext context.Context
	if _, err := ResolveRepositoryName(nilContext, remoteOnly); err == nil {
		t.Fatal("a nil context resolved")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := ResolveRepositoryName(t.Context(), missing); err == nil {
		t.Fatal("a missing directory resolved")
	}
	if _, err := ManifestRepositoryName(missing); err == nil {
		t.Fatal("ManifestRepositoryName resolved a missing directory")
	}
}

func TestResolveRepositoryName_Boundary(t *testing.T) {
	// An origin remote ending in "/." or "/.." names no valid repository (#407).
	for _, remote := range []string{"https://github.com/acme/.", "git@github.com:acme/.."} {
		repo := identityRepo(t, "checkout", remote, "")
		if got, err := ResolveRepositoryName(t.Context(), repo); !errors.Is(err, ErrRepositoryNameInvalid) {
			t.Errorf("remote %s: %q, %v; want ErrRepositoryNameInvalid", remote, got, err)
		}
	}
	// A 100-character name is the limit; one more is invalid.
	limit := strings.Repeat("r", 100)
	if got, err := ManifestRepositoryName(identityRepo(t, "kit", "", "repository:\n  name: "+limit+"\n")); err != nil || got != limit {
		t.Errorf("100-character name: %q, %v", got, err)
	}
	if _, err := ManifestRepositoryName(identityRepo(t, "kit", "", "repository:\n  name: "+limit+"r\n")); !errors.Is(err, ErrRepositoryNameInvalid) {
		t.Errorf("101-character name: %v, want ErrRepositoryNameInvalid", err)
	}
}
