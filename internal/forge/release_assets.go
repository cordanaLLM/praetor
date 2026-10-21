// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// MaxReleaseAssets bounds both release metadata and the smoke check's asset walk.
const MaxReleaseAssets = 1000
const maxReleaseMetadataBytes = 4 << 20

// ReleaseAsset identifies a download through the repository's REST asset endpoint.
type ReleaseAsset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// PublishedRelease is the shipped surface read by ReleaseByTag.
type PublishedRelease struct {
	Tag    string         `json:"tag_name"`
	Draft  bool           `json:"draft"`
	Assets []ReleaseAsset `json:"assets"`
}

// ReleaseByTag reads a published release; unlike mutations, public reads may be anonymous.
func (g *GitHubDriver) ReleaseByTag(ctx context.Context, tag string) (release PublishedRelease, err error) {
	if ctx == nil {
		return release, errors.New("release metadata: context is required")
	}
	if tag == "" || tag == "." || tag == ".." {
		return release, errors.New("release tag is required and cannot be a dot segment")
	}
	path, err := g.repoPath("releases/tags/" + url.PathEscape(tag))
	if err != nil {
		return release, err
	}
	ctx, cancel := context.WithTimeout(ctx, defaultHTTPTimeout)
	defer cancel()
	resp, err := g.openRequest(ctx, http.MethodGet, path, nil, "application/vnd.github+json")
	if err != nil {
		return release, fmt.Errorf("release %s: %w", tag, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("release %s: close metadata: %w", tag, closeErr))
		}
	}()
	return decodeReleaseResponse(resp, tag)
}

func decodeReleaseResponse(resp *http.Response, tag string) (release PublishedRelease, err error) {
	if resp.StatusCode == http.StatusNotFound {
		return release, fmt.Errorf("release %s: not found (404)", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return release, fmt.Errorf("release %s: HTTP %d", tag, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseMetadataBytes+1))
	if err != nil {
		return release, fmt.Errorf("release %s: read metadata: %w", tag, err)
	}
	if len(body) > maxReleaseMetadataBytes {
		return release, fmt.Errorf("release %s: metadata exceeds %d bytes", tag, maxReleaseMetadataBytes)
	}
	if err = json.Unmarshal(body, &release); err != nil {
		return release, fmt.Errorf("release %s: decode metadata: %w", tag, err)
	}
	if release.Tag != tag || release.Draft {
		return release, fmt.Errorf("release %s: not a matching published release", tag)
	}
	if len(release.Assets) > MaxReleaseAssets {
		return release, fmt.Errorf("release %s: asset count exceeds %d", tag, MaxReleaseAssets)
	}
	return release, nil
}

// StreamReleaseAsset copies at most limit bytes, never probing a byte beyond it.
// At an exact limit an unknown Content-Length is refused: EOF cannot be proved in budget.
func (g *GitHubDriver) StreamReleaseAsset(ctx context.Context, id, limit int64, dst io.Writer) (n int64, err error) {
	if ctx == nil || dst == nil {
		return 0, errors.New("release asset: context and destination required")
	}
	if id <= 0 || limit <= 0 {
		return 0, errors.New("release asset: positive id and byte limit required")
	}
	path, err := g.repoPath(fmt.Sprintf("releases/assets/%d", id))
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := g.openRequest(ctx, http.MethodGet, path, nil, "application/octet-stream")
	if err != nil {
		return 0, fmt.Errorf("download release asset %d: %w", id, err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("release asset %d: close body: %w", id, closeErr))
		}
	}()
	return copyReleaseResponse(resp, id, limit, dst)
}

func copyReleaseResponse(resp *http.Response, id, limit int64, dst io.Writer) (n int64, err error) {
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("release asset %d: HTTP %d", id, resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return 0, fmt.Errorf("release asset %d: max-bytes exceeded", id)
	}
	limited := &io.LimitedReader{R: resp.Body, N: limit}
	_, err = io.Copy(dst, limited)
	n = limit - limited.N
	if err != nil {
		return n, fmt.Errorf("stream release asset %d: %w", id, err)
	}
	if n == limit && resp.ContentLength < 0 {
		return n, fmt.Errorf("release asset %d: max-bytes reached without proven EOF", id)
	}
	return n, nil
}
