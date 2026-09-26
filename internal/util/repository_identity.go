// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	maxGitHubOwnerBytes      = 39
	maxGitHubRepositoryBytes = 100
	// DefaultGitHubAPIBase is the REST API root of github.com.
	DefaultGitHubAPIBase = "https://api.github.com"
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

// SplitGitHubRepository parses an "<owner>/<name>" coordinate and validates both parts
// with ValidateGitHubRepositoryIdentity, so a coordinate carrying "..", an extra "/" or a
// URL delimiter never reaches an API path.
func SplitGitHubRepository(coordinate string) (owner, repository string, err error) {
	owner, repository, ok := strings.Cut(coordinate, "/")
	if !ok {
		return "", "", fmt.Errorf("repository %q is not an <owner>/<name> coordinate", coordinate)
	}
	if err = ValidateGitHubRepositoryIdentity(owner, repository); err != nil {
		return "", "", err
	}
	return owner, repository, nil
}

// GitHubAPIBase normalizes a configured GitHub endpoint into a REST API base URL. An empty
// endpoint and the github.com web origin (https://github.com, any case, with or without
// www.) map to DefaultGitHubAPIBase; a trailing slash is dropped. Any other endpoint, such
// as a GitHub Enterprise Server API root or a test server, is returned as given.
func GitHubAPIBase(endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if base == "" || isGitHubWebOrigin(base) {
		return DefaultGitHubAPIBase
	}
	return base
}

// isGitHubWebOrigin reports whether base is the github.com web origin rather than an API root.
func isGitHubWebOrigin(base string) bool {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}
	host := strings.ToLower(parsed.Host)
	return host == "github.com" || host == "www.github.com"
}
