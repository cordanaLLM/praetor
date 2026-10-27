// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxReusableWorkflowBytes bounds the workflow document read from the forge (HISS-02): 256 KiB.
	maxReusableWorkflowBytes = 256 * 1024
	// reusableWorkflowTimeout bounds one forge read of an external reusable workflow (HISS-02).
	reusableWorkflowTimeout = 20 * time.Second
)

// ReusableWorkflowReader reads an external reusable workflow document from the forge.
// Order: engine-hiss18-cross-repo-reusable-20261027.
type ReusableWorkflowReader interface {
	ReusableWorkflowAt(ctx context.Context, owner, repo, path, sha string) ([]byte, error)
}

// validateReusableWorkflowCoordinates checks coordinate fields before any request is made.
func validateReusableWorkflowCoordinates(owner, repo, path, sha string) (string, error) {
	if !IsFullCommitSHA(sha) {
		return "", fmt.Errorf("reusable workflow: commit SHA %q is not a full 40-hex SHA", sha)
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, repo); err != nil {
		return "", fmt.Errorf("reusable workflow: %w", err)
	}
	if !strings.HasPrefix(path, plannedWorkflowDir) || strings.Contains(path, "..") || path == plannedWorkflowDir {
		return "", fmt.Errorf("reusable workflow: path %q outside %s", path, plannedWorkflowDir)
	}
	escapedPath, err := escapeSegments(path)
	if err != nil {
		return "", fmt.Errorf("reusable workflow: %w", err)
	}
	return escapedPath, nil
}

// ReusableWorkflowAt fetches a reusable workflow from GitHub through GET /repos/{owner}/{repo}/contents/{path}?ref={sha}.
// Order: engine-hiss18-cross-repo-reusable-20261027.
func (g *GitHubDriver) ReusableWorkflowAt(ctx context.Context, owner, repo, path, sha string) (workflowBytes []byte, err error) {
	if ctx == nil {
		return nil, errors.New("reusable workflow: context required")
	}
	escapedPath, err := validateReusableWorkflowCoordinates(owner, repo, path, sha)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, reusableWorkflowTimeout)
	defer cancel()

	reqPath := fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s",
		url.PathEscape(owner), url.PathEscape(repo), escapedPath, url.QueryEscape(sha))
	resp, err := g.openRequest(ctx, http.MethodGet, reqPath, nil, "application/vnd.github.raw+json")
	if err != nil {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: %w", owner, repo, path, sha, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("reusable workflow: close body: %w", closeErr))
		}
	}()
	return readReusableWorkflowResponse(resp, owner, repo, path, sha)
}

// readReusableWorkflowResponse reads and checks the response bytes, enforcing the 256 KiB bound.
func readReusableWorkflowResponse(resp *http.Response, owner, repo, path, sha string) ([]byte, error) {
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: %w", owner, repo, path, sha, ErrRepositoryNotVisible)
	}
	if resp.ContentLength > maxReusableWorkflowBytes {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: content length %d exceeds %d bytes",
			owner, repo, path, sha, resp.ContentLength, maxReusableWorkflowBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxReusableWorkflowBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: read: %w", owner, repo, path, sha, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: HTTP %d: %s",
			owner, repo, path, sha, resp.StatusCode, util.BodyPreview(body))
	}
	if len(body) > maxReusableWorkflowBytes {
		return nil, fmt.Errorf("reusable workflow %s/%s/%s@%s: response body exceeds %d bytes",
			owner, repo, path, sha, maxReusableWorkflowBytes)
	}
	return body, nil
}
