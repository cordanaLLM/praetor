// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxMergedPRsLimit bounds the number of merged pull requests parsed or loaded (HISS-02).
	MaxMergedPRsLimit = 1000
	// MaxMergedPRBytes bounds the bytes read when loading merged PR fixtures (HISS-02).
	MaxMergedPRBytes = 16 * 1024 * 1024
)

// MergedPullRequest represents a landed pull request and its closing issues.
type MergedPullRequest struct {
	Number        int            `json:"number"`
	HeadBranch    string         `json:"head_branch"`
	Title         string         `json:"title"`
	Milestone     string         `json:"milestone,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	MergedAt      time.Time      `json:"merged_at"`
	ClosingIssues []ClosingIssue `json:"closing_issues,omitempty"`
}

// ClosingIssue represents an issue linked to and closed by a pull request.
type ClosingIssue struct {
	Number    int        `json:"number"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// closingKeywordRegex matches closing keywords like "closes #123", "fixes #123", "resolves #123".
var closingKeywordRegex = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s+(?:([a-zA-Z0-9_\-\.]+)/)?([a-zA-Z0-9_\-\.]+)?#(\d+)`)

// ParseClosingIssueNumbers extracts closing issue numbers from a pull request body.
func ParseClosingIssueNumbers(body string) []int {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	lines := strings.Split(body, "\n")
	var nums []int
	seen := make(map[int]bool)
	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		matches := closingKeywordRegex.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(nums) >= MaxDependenciesLimit {
				return nums
			}
			num, err := strconv.Atoi(m[3])
			if err != nil || num <= 0 || seen[num] {
				continue
			}
			seen[num] = true
			nums = append(nums, num)
		}
	}
	return nums
}

// ReadMergedPullRequests reads and decodes a JSON stream of merged pull requests, bounded by MaxMergedPRBytes.
func ReadMergedPullRequests(r io.Reader) ([]MergedPullRequest, error) {
	if r == nil {
		return nil, errors.New("reader is nil")
	}
	limited := io.LimitReader(r, MaxMergedPRBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read merged pull requests: %w", err)
	}
	if int64(len(data)) > MaxMergedPRBytes {
		return nil, fmt.Errorf("merged pull requests data exceeds %d bytes limit", MaxMergedPRBytes)
	}
	return ParseMergedPullRequests(data)
}

// ParseMergedPullRequests parses JSON bytes into a slice of MergedPullRequest records.
func ParseMergedPullRequests(data []byte) ([]MergedPullRequest, error) {
	trimmed := strings.TrimSpace(string(data))
	if len(trimmed) == 0 {
		return nil, errors.New("merged pull requests data is empty")
	}
	var prs []MergedPullRequest
	if err := json.Unmarshal(data, &prs); err != nil {
		return nil, fmt.Errorf("parse merged pull requests: %w", err)
	}
	if len(prs) > MaxMergedPRsLimit {
		return nil, fmt.Errorf("merged pull requests count %d exceeds limit %d", len(prs), MaxMergedPRsLimit)
	}
	return prs, nil
}

// ReadMergedPullRequestsFile reads a local JSON file containing merged pull request records.
func ReadMergedPullRequestsFile(path string) ([]MergedPullRequest, error) {
	cleanPath := strings.TrimSpace(path)
	if cleanPath == "" {
		return nil, errors.New("empty path for merged pull requests file")
	}
	data, err := util.ReadFileLimited(cleanPath, MaxMergedPRBytes)
	if err != nil {
		return nil, fmt.Errorf("read merged pull requests file %s: %w", cleanPath, err)
	}
	return ParseMergedPullRequests(data)
}
