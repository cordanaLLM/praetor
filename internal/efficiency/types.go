// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"time"
)

const (
	// NotMeasured is the string printed when a source is missing or metric is unmeasured.
	NotMeasured = "not measured"

	// FollowUpRefs is the pointer string for fact-hit ratio and checks-before-reviews in Slice 1.
	FollowUpRefs = "not measured (see #897)"

	// MaxSourceFiles bounds directory file traversal (HISS-02). One agent project directory
	// holds thousands of session files (4800 in the largest measured), so the bound sits well above.
	MaxSourceFiles = 20000

	// MaxSourceLines bounds lines read per file (HISS-02). Long sessions reach 70000 lines.
	MaxSourceLines = 1000000

	// MaxFileBytes bounds the bytes read from a single source file (HISS-02). The largest
	// measured session is 113 MB. Exceeding any of these bounds fails the run.
	MaxFileBytes = 256 * 1024 * 1024
)

// UnitReport represents efficiency metrics for one landed pull request.
type UnitReport struct {
	PullRequestNumber          int       `json:"pull_request_number"`
	HeadBranch                 string    `json:"head_branch"`
	Title                      string    `json:"title"`
	Milestone                  string    `json:"milestone,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	MergedAt                   time.Time `json:"merged_at"`
	ClosingIssues              []int     `json:"closing_issues,omitempty"`
	IssueToMerge               string    `json:"issue_to_merge"`
	IssueToMergeSecs           *int64    `json:"issue_to_merge_secs,omitempty"`
	FrontierTokens             string    `json:"frontier_tokens"`
	FrontierTokensNum          *int64    `json:"frontier_tokens_num,omitempty"`
	Spend                      string    `json:"spend"`
	SpendAmount                *float64  `json:"spend_amount,omitempty"`
	OperatorTouches            string    `json:"operator_touches"`
	OperatorTouchNum           *int      `json:"operator_touches_num,omitempty"`
	PromptCacheHitRate         string    `json:"prompt_cache_hit_rate"`
	CacheHitRatio              *float64  `json:"cache_hit_ratio,omitempty"`
	LocalFirstRatio            string    `json:"local_first_ratio"`
	LocalRatio                 *float64  `json:"local_ratio,omitempty"`
	FactHitRatio               string    `json:"fact_hit_ratio"`
	ChecksBeforeReviews        string    `json:"checks_before_reviews"`
	Sources                    string    `json:"sources"`
	TranscriptRequestsNotAdded *int      `json:"transcript_requests_not_added,omitempty"`
}

// MilestoneSummary aggregates metrics across units in a milestone.
type MilestoneSummary struct {
	Milestone            string   `json:"milestone,omitempty"`
	UnitsCount           int      `json:"units_count"`
	FrontierTokens       string   `json:"frontier_tokens"`
	FrontierTokensNum    *int64   `json:"frontier_tokens_num,omitempty"`
	AttributedSpend      string   `json:"attributed_spend"`
	AttributedSpendNum   *float64 `json:"attributed_spend_num,omitempty"`
	UnattributedSpend    string   `json:"unattributed_spend"`
	UnattributedSpendNum *float64 `json:"unattributed_spend_num,omitempty"`
	TotalSpend           string   `json:"total_spend"`
	TotalSpendNum        *float64 `json:"total_spend_num,omitempty"`
	OtherUnitsSpend      string   `json:"other_units_spend"`
	OtherUnitsSpendNum   *float64 `json:"other_units_spend_num,omitempty"`
	IssueToMergeUnits    int      `json:"issue_to_merge_units"`
	AvgIssueToMerge      string   `json:"avg_issue_to_merge"`
	AvgIssueToMergeSecs  *int64   `json:"avg_issue_to_merge_secs,omitempty"`
	OperatorTouches      string   `json:"operator_touches"`
	OperatorTouchNum     *int     `json:"operator_touches_num,omitempty"`
	PromptCacheHitRate   string   `json:"prompt_cache_hit_rate"`
	CacheHitRatio        *float64 `json:"cache_hit_ratio,omitempty"`
	LocalFirstRatio      string   `json:"local_first_ratio"`
	LocalRatio           *float64 `json:"local_ratio,omitempty"`
	FactHitRatio         string   `json:"fact_hit_ratio"`
	ChecksBeforeReviews  string   `json:"checks_before_reviews"`
}

// SourcesMeasured records which of the optional sources were present and read.
type SourcesMeasured struct {
	Forge       bool `json:"forge"`
	Transcripts bool `json:"transcripts"`
	SpendLog    bool `json:"spend_log"`
}

// Report holds the complete efficiency ledger output.
type Report struct {
	Units            []UnitReport     `json:"units"`
	MilestoneSummary MilestoneSummary `json:"milestone_summary"`
	Sources          SourcesMeasured  `json:"sources_measured"`
	Notes            []string         `json:"notes,omitempty"`
}
