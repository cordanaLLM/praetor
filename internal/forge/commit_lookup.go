// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrCommitNotFound is returned by CommitAt when the repository has no commit the ref names:
// a SHA it does not carry, or a tag it does not have. GitHub answers both with 422 "No commit
// found for SHA".
var ErrCommitNotFound = errors.New("no commit found")

// ErrRepositoryNotVisible is returned by CommitAt when GitHub answers 404: the repository does
// not exist, or the token cannot read it. The two are indistinguishable from outside.
var ErrRepositoryNotVisible = errors.New("repository not found or not visible to this token")

// maxTagPages bounds the tag listing TagsAt reads (HISS-02): at most 1000 tags.
const maxTagPages = 10

// fullCommitSHA is a full, lowercase commit SHA as the REST API reports one.
var fullCommitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// CommitAt resolves ref in the driver's repository to the full SHA of the commit it names,
// through GET /repos/{owner}/{repo}/commits/{ref}. ref is a commit SHA or a fully qualified
// tag, refs/tags/<name>: that form peels an annotated tag to its commit and never matches a
// branch of the same name. It returns ErrCommitNotFound when the repository has no such
// commit or tag, ErrRepositoryNotVisible on 404, and any other answer (a rate limit, a
// server error, a transport failure, a missing token) as an error, so a caller never reads a
// question it could not ask as a missing commit.
func (g *GitHubDriver) CommitAt(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("resolve commit: ref cannot be empty")
	}
	escaped, err := escapeSegments(ref)
	if err != nil {
		return "", fmt.Errorf("resolve commit: %w", err)
	}
	path, err := g.repoPath("commits/" + escaped)
	if err != nil {
		return "", err
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("resolve commit %s: %w", ref, err)
	}
	switch {
	case status == http.StatusOK:
		return decodeCommitSHA(ref, body)
	case status == http.StatusUnprocessableEntity && bytes.Contains(body, []byte("No commit found")):
		return "", fmt.Errorf("resolve commit %s: %w", ref, ErrCommitNotFound)
	case status == http.StatusNotFound:
		return "", fmt.Errorf("resolve commit %s: %w", ref, ErrRepositoryNotVisible)
	default:
		return "", fmt.Errorf("resolve commit %s: unexpected status %d: %s", ref, status, util.BodyPreview(body))
	}
}

// decodeCommitSHA reads the sha field of the commit the REST API returned for ref.
func decodeCommitSHA(ref string, body []byte) (string, error) {
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return "", fmt.Errorf("resolve commit %s: decode response: %w", ref, err)
	}
	if !fullCommitSHA.MatchString(commit.SHA) {
		return "", fmt.Errorf("resolve commit %s: response names no full commit SHA: %q", ref, util.TruncateExcerpt(commit.SHA, 80))
	}
	return commit.SHA, nil
}

// TagsAt returns the names of the driver's repository tags that point at commit, as
// GET /repos/{owner}/{repo}/tags lists them, each peeled to its commit. It reads at most
// maxTagPages pages; a longer listing is an error, so an empty answer always means that no
// tag points at the commit.
func (g *GitHubDriver) TagsAt(ctx context.Context, commit string) ([]string, error) {
	base, err := g.repoPath("tags")
	if err != nil {
		return nil, err
	}
	var names []string
	err = g.walkPages(ctx, base, "", "repository tags", maxTagPages, func(body []byte) (int, bool, error) {
		var tags []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(body, &tags); err != nil {
			return 0, false, fmt.Errorf("decode repository tags: %w", err)
		}
		for _, tag := range tags {
			if tag.Commit.SHA == commit {
				names = append(names, tag.Name)
			}
		}
		return len(tags), false, nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// escapeSegments path-escapes each "/"-separated segment of ref, so a tag name can never
// carry a query or a fragment into the request path. An empty, "." or ".." segment is
// refused: it would name another path than the ref.
func escapeSegments(ref string) (string, error) {
	segments := strings.Split(ref, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("ref %q has an empty or dot path segment", ref)
		}
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/"), nil
}
