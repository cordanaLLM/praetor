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
