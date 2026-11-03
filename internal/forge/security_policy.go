// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// PrivateVulnerabilityReporting reads GitHub's repository reporting setting through the
// existing authenticated client. Missing or malformed state is unread, never disabled.
// Source: https://docs.github.com/en/rest/repos/repos#check-if-private-vulnerability-reporting-is-enabled-for-a-repository
func (g *GitHubDriver) PrivateVulnerabilityReporting(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, defaultHTTPTimeout)
	defer cancel()
	path, err := g.repoPath("private-vulnerability-reporting")
	if err != nil {
		return false, fmt.Errorf("read private vulnerability reporting: %w", err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, fmt.Errorf("read private vulnerability reporting: %w", err)
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("read private vulnerability reporting: unexpected status %d", status)
	}
	var state struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return false, fmt.Errorf("decode private vulnerability reporting: %w", err)
	}
	if state.Enabled == nil {
		return false, fmt.Errorf("private vulnerability reporting response names no enabled boolean")
	}
	return *state.Enabled, nil
}
