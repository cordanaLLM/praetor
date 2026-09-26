// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

func TestValidateGitHubRepositoryIdentityPositive(t *testing.T) {
	for _, identity := range [][2]string{
		{"cordanaLLM", "praetor"},
		{"a", ".github"},
		{strings.Repeat("a", maxGitHubOwnerBytes), strings.Repeat("r", maxGitHubRepositoryBytes)},
		{"owner-name", "repo.with_under-score"},
	} {
		if err := ValidateGitHubRepositoryIdentity(identity[0], identity[1]); err != nil {
			t.Fatalf("valid identity %q/%q: %v", identity[0], identity[1], err)
		}
	}
}

func TestValidateGitHubRepositoryIdentityNegativeAndBoundary(t *testing.T) {
	for _, identity := range [][2]string{
		{"", "repo"}, {"owner", ""},
		{"-owner", "repo"}, {"owner-", "repo"}, {"owner--name", "repo"},
		{"owner_name", "repo"}, {strings.Repeat("o", maxGitHubOwnerBytes+1), "repo"},
		{"owner", "."}, {"owner", ".."}, {"owner", strings.Repeat("r", maxGitHubRepositoryBytes+1)},
		{"owner/name", "repo"}, {"owner", "repo/name"}, {"owner", `repo\name`},
		{"owner", "repo\rname"}, {"owner", "repo\nname"}, {"owner", "repo]name"},
		{"owner", "repo(name"}, {"owner", "repo name"},
	} {
		if err := ValidateGitHubRepositoryIdentity(identity[0], identity[1]); err == nil {
			t.Fatalf("invalid identity %q/%q accepted", identity[0], identity[1])
		}
	}
}

func TestSplitGitHubRepository_Positive(t *testing.T) {
	owner, repository, err := SplitGitHubRepository("cordanaLLM/praetor")
	if err != nil || owner != "cordanaLLM" || repository != "praetor" {
		t.Fatalf("SplitGitHubRepository = %q, %q, %v", owner, repository, err)
	}
}

func TestSplitGitHubRepository_NegativeAndBoundary(t *testing.T) {
	for _, coordinate := range []string{
		"", "no-slash", "/repo", "owner/", "owner/..", "owner/.", "../repo",
		"owner/repo/extra", "owner/repo?x=1", "owner/repo#frag", "own..er/repo",
	} {
		if owner, repository, err := SplitGitHubRepository(coordinate); err == nil {
			t.Fatalf("invalid coordinate %q accepted as %q/%q", coordinate, owner, repository)
		}
	}
	long := strings.Repeat("o", maxGitHubOwnerBytes) + "/" + strings.Repeat("r", maxGitHubRepositoryBytes)
	if _, _, err := SplitGitHubRepository(long); err != nil {
		t.Fatalf("coordinate at both length limits rejected: %v", err)
	}
}

func TestGitHubAPIBase_Positive(t *testing.T) {
	for endpoint, want := range map[string]string{
		"":                            DefaultGitHubAPIBase,
		"https://github.com":          DefaultGitHubAPIBase,
		"https://github.com/":         DefaultGitHubAPIBase,
		"HTTPS://GitHub.com":          DefaultGitHubAPIBase,
		"https://www.github.com":      DefaultGitHubAPIBase,
		"https://api.github.com/":     DefaultGitHubAPIBase,
		"https://ghe.example/api/v3/": "https://ghe.example/api/v3",
		"  http://127.0.0.1:8080  ":   "http://127.0.0.1:8080",
	} {
		if got := GitHubAPIBase(endpoint); got != want {
			t.Fatalf("GitHubAPIBase(%q) = %q, want %q", endpoint, got, want)
		}
	}
}

func TestGitHubAPIBase_NegativeAndBoundary(t *testing.T) {
	// Only the bare web origin is rewritten: a path, userinfo, another host or scheme is
	// an explicit endpoint and stays as configured.
	for _, endpoint := range []string{
		"https://github.com/api/v3", "https://user@github.com", "https://github.com.evil.example",
		"ftp://github.com", "https://gist.github.com",
	} {
		if got := GitHubAPIBase(endpoint); got == DefaultGitHubAPIBase {
			t.Fatalf("GitHubAPIBase(%q) rewrote an explicit endpoint to %q", endpoint, got)
		}
	}
	if got := GitHubAPIBase("/"); got != DefaultGitHubAPIBase {
		t.Fatalf("a bare slash is empty after trimming, got %q", got)
	}
}
