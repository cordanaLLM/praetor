// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package forge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxMergedPRsLimit bounds the number of merged pull requests parsed or loaded (HISS-02).
	MaxMergedPRsLimit = 1000
	// MaxMergedPRBytes bounds the bytes read when loading merged PR fixtures (HISS-02).
	MaxMergedPRBytes = 16 * 1024 * 1024
)

// MergedPullRequest represents a landed pull request and its closing issues. Disposition, Lane,
// MetricEpoch and the vector fields come only from a records file; a live listing leaves them
// empty. The vector fields stay raw JSON here: the efficiency ledger is their one parser. A field
// absent from the record is nil, an explicit null is the four bytes "null".
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
	TokensByProvider json.RawMessage `json:"tokens_by_provider,omitempty"`
	WallSeconds      json.RawMessage `json:"wall_seconds,omitempty"`
	ReviewRounds     json.RawMessage `json:"review_rounds,omitempty"`
	Retries          json.RawMessage `json:"retries,omitempty"`
	OperatorMinutes  json.RawMessage `json:"operator_minutes,omitempty"`
	EscapedDefects   json.RawMessage `json:"escaped_defects,omitempty"`
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

// mergedPRJSON is the strict reader of a records file: unknown or repeated member names, a
// second document and nesting beyond the record shape are refused.
var mergedPRJSON = strictjson.Options{MaxBytes: MaxMergedPRBytes, MaxDepth: 8}

// ParseMergedPullRequests parses a records file: a JSON array of records, or an object whose
// only member is "units" holding that array.
func ParseMergedPullRequests(data []byte) ([]MergedPullRequest, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("merged pull requests data is empty")
	}
	prs, err := decodeMergedPullRequests(trimmed)
	if err != nil {
		return nil, err
	}
	if len(prs) > MaxMergedPRsLimit {
		return nil, fmt.Errorf("merged pull requests count %d exceeds limit %d", len(prs), MaxMergedPRsLimit)
	}
	if err := normalizePRNumbers(prs); err != nil {
		return nil, err
	}
	return prs, nil
}

func decodeMergedPullRequests(data []byte) ([]MergedPullRequest, error) {
	if data[0] == '{' {
		var wrapper struct {
			Units []MergedPullRequest `json:"units"`
		}
		if err := strictjson.Decode(data, &wrapper, mergedPRJSON); err != nil {
			return nil, fmt.Errorf("parse merged pull requests wrapper: %w", err)
		}
		if wrapper.Units == nil {
			return nil, errors.New(`parse merged pull requests wrapper: the object carries no "units" array`)
		}
		return wrapper.Units, nil
	}
	var prs []MergedPullRequest
	if err := strictjson.Decode(data, &prs, mergedPRJSON); err != nil {
		return nil, fmt.Errorf("parse merged pull requests: %w", err)
	}
	if prs == nil {
		return nil, errors.New(`parse merged pull requests: want a JSON array or {"units": [...]}, got null`)
	}
	return prs, nil
}

// normalizePRNumbers fills number from its alias pull_request_number. A record without either,
// or with both naming different pull requests, is refused.
func normalizePRNumbers(prs []MergedPullRequest) error {
	for i := range prs {
		pr := &prs[i]
		switch {
		case pr.Number == 0 && pr.PRNumber == 0:
			return fmt.Errorf("merged pull request record %d carries no number", i+1)
		case pr.Number != 0 && pr.PRNumber != 0 && pr.Number != pr.PRNumber:
			return fmt.Errorf("merged pull request record %d: number %d and pull_request_number %d name different pull requests", i+1, pr.Number, pr.PRNumber)
		case pr.Number == 0:
			pr.Number = pr.PRNumber
		}
	}
	return nil
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
