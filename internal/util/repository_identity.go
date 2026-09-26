// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxGitHubOwnerBytes      = 39
	maxGitHubRepositoryBytes = 100
)

var (
	githubOwnerSegment      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	githubRepositorySegment = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ValidateGitHubRepositoryIdentity rejects owner/name pairs that are unsafe or invalid
// in GitHub API, workflow-status, and Markdown link paths.
func ValidateGitHubRepositoryIdentity(owner, repository string) error {
	if len(owner) > maxGitHubOwnerBytes || !githubOwnerSegment.MatchString(owner) || strings.Contains(owner, "--") {
		return fmt.Errorf("invalid GitHub repository owner %q", owner)
	}
	if len(repository) > maxGitHubRepositoryBytes || !githubRepositorySegment.MatchString(repository) ||
		repository == "." || repository == ".." {
		return fmt.Errorf("invalid GitHub repository name %q", repository)
	}
	return nil
}
