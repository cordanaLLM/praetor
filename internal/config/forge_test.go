// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"errors"
	"strings"
	"testing"
)

// forgeManifest is a manifest declaring forge under its repository block; "" declares none.
func forgeManifest(forge string) string {
	manifest := "version: 1\nrepository:\n  owner: acme\n  name: kit\n"
	if forge != "" {
		manifest += "  forge: " + forge + "\n"
	}
	return manifest
}

// Positive: every documented forge decodes through the strict decoder, and a declaration wins
// over the origin remote's host.
func TestForge_Positive_DeclaredForgeDecodesAndWins(t *testing.T) {
	for _, forge := range forges {
		manifest, err := ParseManifest("forge.yaml", []byte(forgeManifest(string(forge))))
		if err != nil || manifest.Repository.Forge != forge {
			t.Fatalf("forge %s: %+v, %v", forge, manifest, err)
		}
		repo := identityRepo(t, "kit", "https://code.example.org/acme/kit.git", forgeManifest(string(forge)))
		resolved, err := ResolveRepositoryForge(t.Context(), repo)
		if err != nil || resolved != forge {
			t.Fatalf("declared %s on a non-GitHub host resolved %q, %v", forge, resolved, err)
		}
	}
	github := identityRepo(t, "kit", "git@github.com:acme/kit.git", forgeManifest("forgejo"))
	if resolved, err := ResolveRepositoryForge(t.Context(), github); err != nil || resolved != ForgeForgejo {
		t.Fatalf("a declaration must win over a github.com remote: %q, %v", resolved, err)
	}
}

// Negative: an unknown, mis-cased, empty or non-string forge is refused by the strict decoder
// with the key named, and a non-GitHub host without a declaration is refused naming the key.
func TestForge_Negative_RefusesUnknownAndUndeclared(t *testing.T) {
	for _, value := range []string{"gitea", "GitHub", `""`, "[github]", "true"} {
		_, err := DecodeManifest([]byte(forgeManifest(value)))
		if err == nil || !strings.Contains(err.Error(), ForgeKey) {
			t.Errorf("forge %s: %v, want a refusal naming %s", value, err, ForgeKey)
		}
	}
	repo := identityRepo(t, "kit", "https://code.example.org/acme/kit.git", forgeManifest(""))
	_, err := ResolveRepositoryForge(t.Context(), repo)
	if !errors.Is(err, ErrForgeUndeclared) || !strings.Contains(err.Error(), ForgeKey) ||
		!strings.Contains(err.Error(), "code.example.org") {
		t.Fatalf("undeclared forge on a non-GitHub host: %v, want ErrForgeUndeclared naming %s and the host", err, ForgeKey)
	}
	if _, err := RepositoryForge("gitea", ""); err == nil || !strings.Contains(err.Error(), ForgeKey) {
		t.Fatalf("a programmatic unknown forge must be refused naming the key: %v", err)
	}
	broken := identityRepo(t, "kit", "https://github.com/acme/kit.git", "repository: [\n")
	if _, err := ResolveRepositoryForge(t.Context(), broken); err == nil || errors.Is(err, ErrForgeUndeclared) {
		t.Fatalf("an unreadable manifest must be reported, not resolved from the remote: %v", err)
	}
}

// Boundary: an omitted key on a github.com remote is GitHub; a remote host that only resembles
// github.com, and a repository without any network origin remote, need the declaration.
func TestForge_Boundary_DefaultOnlyForGitHubHost(t *testing.T) {
	for _, remote := range []string{"https://github.com/acme/kit.git", "git@github.com:acme/kit.git", "https://GitHub.com/acme/kit.git"} {
		repo := identityRepo(t, "kit", remote, forgeManifest(""))
		if resolved, err := ResolveRepositoryForge(t.Context(), repo); err != nil || resolved != ForgeGitHub {
			t.Errorf("remote %s: %q, %v, want github", remote, resolved, err)
		}
	}
	for _, remote := range []string{"https://github.com.example.org/acme/kit.git", "https://ghe.example.com/acme/kit.git", ""} {
		repo := identityRepo(t, "kit", remote, forgeManifest(""))
		if _, err := ResolveRepositoryForge(t.Context(), repo); !errors.Is(err, ErrForgeUndeclared) {
			t.Errorf("remote %q: %v, want ErrForgeUndeclared", remote, err)
		}
	}
	if forge, err := RepositoryForge("", githubHost); err != nil || forge != ForgeGitHub {
		t.Fatalf("github.com host: %q, %v", forge, err)
	}
	// An explicit null declares nothing, as an omitted key does.
	if manifest, err := DecodeManifest([]byte(forgeManifest("~"))); err != nil || manifest.Repository.Forge != "" {
		t.Fatalf("forge ~: %+v, %v, want undeclared", manifest, err)
	}
	rendered, err := RenderManifest(&Manifest{Version: 1, Repository: RepositoryMetadata{Owner: "acme", Name: "kit"}})
	if err != nil || strings.Contains(string(rendered), "forge") {
		t.Fatalf("an undeclared forge must not be rendered: %s, %v", rendered, err)
	}
	if _, err := ResolveRepositoryForge(nil, t.TempDir()); err == nil { //nolint:staticcheck // a nil context is the refused input
		t.Fatal("a nil context must be refused")
	}
}
