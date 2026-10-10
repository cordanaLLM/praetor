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

// VectorFieldRaw captures raw vector values and provenance from JSON fixtures.
type VectorFieldRaw struct {
	Value      json.RawMessage `json:"value"`
	Provenance string          `json:"provenance"`
}

// UnmarshalJSON unmarshals either an object with value and provenance or raw value bytes.
func (v *VectorFieldRaw) UnmarshalJSON(data []byte) error {
	var obj struct {
		Value      json.RawMessage `json:"value"`
		Provenance string          `json:"provenance"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && len(obj.Value) > 0 {
		v.Value = obj.Value
		v.Provenance = obj.Provenance
		return nil
	}
	v.Value = data
	v.Provenance = ""
	return nil
}

// MergedPullRequest represents a landed pull request and its closing issues.
type MergedPullRequest struct {
	Number           int             `json:"number"`
	PRNumber         int             `json:"pull_request_number,omitempty"`
	HeadBranch       string          `json:"head_branch"`
	Title            string          `json:"title"`
	Milestone        string          `json:"milestone,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	MergedAt         time.Time       `json:"merged_at"`
	ClosingIssues    []ClosingIssue  `json:"closing_issues,omitempty"`
	Disposition      string          `json:"disposition,omitempty"`
	Lane             string          `json:"lane,omitempty"`
	MetricEpoch      string          `json:"metric_epoch,omitempty"`
	TokensByProvider *VectorFieldRaw `json:"tokens_by_provider,omitempty"`
	WallSeconds      *VectorFieldRaw `json:"wall_seconds,omitempty"`
	ReviewRounds     *VectorFieldRaw `json:"review_rounds,omitempty"`
	Retries          *VectorFieldRaw `json:"retries,omitempty"`
	OperatorMinutes  *VectorFieldRaw `json:"operator_minutes,omitempty"`
	EscapedDefects   *VectorFieldRaw `json:"escaped_defects,omitempty"`
}

// EffectiveNumber returns the PR number, checking pull_request_number if number is zero.
func (m MergedPullRequest) EffectiveNumber() int {
	if m.Number != 0 {
		return m.Number
	}
	return m.PRNumber
}

// ClosingIssue represents an issue linked to and closed by a pull request.
type ClosingIssue struct {
	Number    int        `json:"number"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// MergedPullRequestQuery selects landed pull requests. Milestone filters before Limit applies.
type MergedPullRequestQuery struct {
	Limit     int
	Milestone string
}

// MergedPullRequestList is a listing result. Truncated is non-empty when the listing is
// incomplete and says why; Warnings name pull requests or issues that could not be read.
type MergedPullRequestList struct {
	PullRequests []MergedPullRequest
	Truncated    string
	Warnings     []string
}

// closingKeywordRegex matches closing keywords like "closes #123", "fixes #123", "resolves #123".
var closingKeywordRegex = regexp.MustCompile(`(?i)\b(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s+(?:([a-zA-Z0-9_\-\.]+)/)?([a-zA-Z0-9_\-\.]+)?#(\d+)`)

// localClosingNumbers returns the issue numbers one line closes in this repository.
func localClosingNumbers(line string) []int {
	var nums []int
	for _, m := range closingKeywordRegex.FindAllStringSubmatch(line, -1) {
		// owner/repo#N points at another repository; its number means nothing here.
		if m[1] != "" || m[2] != "" {
			continue
		}
		if num, err := strconv.Atoi(m[3]); err == nil && num > 0 {
			nums = append(nums, num)
		}
	}
	return nums
}

// ParseClosingIssueNumbers extracts closing issue numbers from a pull request body.
func ParseClosingIssueNumbers(body string) []int {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	lines := strings.Split(body, "\n")
	var nums []int
	seen := make(map[int]bool)
	for i := 0; i < len(lines) && i < MaxLinesLimit; i++ {
		for _, num := range localClosingNumbers(strings.TrimSpace(lines[i])) {
			if seen[num] {
				continue
			}
			if len(nums) >= MaxDependenciesLimit {
				return nums
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
	if strings.HasPrefix(trimmed, "{") {
		var wrapper struct {
			Units []MergedPullRequest `json:"units"`
		}
		if err := json.Unmarshal(data, &wrapper); err == nil && len(wrapper.Units) > 0 {
			prs = wrapper.Units
		}
	}
	if prs == nil {
		if err := json.Unmarshal(data, &prs); err != nil {
			return nil, fmt.Errorf("parse merged pull requests: %w", err)
		}
	}
	if len(prs) > MaxMergedPRsLimit {
		return nil, fmt.Errorf("merged pull requests count %d exceeds limit %d", len(prs), MaxMergedPRsLimit)
	}
	normalizePRNumbers(prs)
	return prs, nil
}

func normalizePRNumbers(prs []MergedPullRequest) {
	for i := range prs {
		if prs[i].Number == 0 && prs[i].PRNumber != 0 {
			prs[i].Number = prs[i].PRNumber
		}
	}
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
