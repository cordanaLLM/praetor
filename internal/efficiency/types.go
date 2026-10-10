// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// NotMeasured is the string printed when a source is missing or metric is unmeasured.
	NotMeasured = "not measured"

	// FollowUpRefs is the pointer string for fact-hit ratio and checks-before-reviews in Slice 1.
	FollowUpRefs = "not measured (see #897)"

	// UndefinedRate is printed when a rate's denominator is zero (e.g. empty ledger or zero qualified units).
	UndefinedRate = "undefined"

	// CurrentMetricEpoch identifies the schema version for efficiency metrics (Issue #1132).
	CurrentMetricEpoch = "2026-10-10"

	// MaxSourceFiles bounds directory file traversal (HISS-02). One agent project directory
	// holds thousands of session files (4800 in the largest measured), so the bound sits well above.
	MaxSourceFiles = 20000

	// MaxSourceLines bounds lines read per file (HISS-02). Long sessions reach 70000 lines.
	MaxSourceLines = 1000000

	// MaxFileBytes bounds the bytes read from a single source file (HISS-02). The largest
	// measured session is 113 MB. Exceeding any of these bounds fails the run.
	MaxFileBytes = 256 * 1024 * 1024
)

// Provenance classifies the source and certainty of a metric value.
type Provenance string

const (
	ProvenanceMeasured Provenance = "measured"
	ProvenanceModeled  Provenance = "modeled"
	ProvenanceCited    Provenance = "cited"
	ProvenanceInterval Provenance = "interval"
)

// ValidProvenance checks whether p is one of the four allowed provenance labels.
func ValidProvenance(p Provenance) bool {
	switch p {
	case ProvenanceMeasured, ProvenanceModeled, ProvenanceCited, ProvenanceInterval:
		return true
	default:
		return false
	}
}

// Dispositions for unit outcomes.
const (
	DispositionQualified = "qualified"
	DispositionOffered   = "offered"
	DispositionRejected  = "rejected"
	DispositionAbandoned = "abandoned"
	DispositionReverted  = "reverted"
	DispositionTimedOut  = "timed-out"
)

// normalizeDisposition standardizes disposition strings (handling underscores/dashes).
func normalizeDisposition(disp string) string {
	d := strings.ToLower(strings.TrimSpace(disp))
	d = strings.ReplaceAll(d, "_", "-")
	switch d {
	case "qualified", "pass", "passed":
		return DispositionQualified
	case "reverted", "revert":
		return DispositionReverted
	case "rejected", "reject":
		return DispositionRejected
	case "abandoned", "abandon":
		return DispositionAbandoned
	case "timed-out", "timedout", "timeout":
		return DispositionTimedOut
	case "offered":
		return DispositionOffered
	default:
		return d
	}
}

// ValidDisposition checks if disp is a recognized unit outcome disposition.
func ValidDisposition(disp string) bool {
	switch normalizeDisposition(disp) {
	case DispositionQualified, DispositionOffered, DispositionRejected, DispositionAbandoned, DispositionReverted, DispositionTimedOut:
		return true
	default:
		return false
	}
}

// VectorField represents a metric value paired with its required provenance label.
type VectorField[T any] struct {
	Value      T          `json:"value"`
	Provenance Provenance `json:"provenance"`
}

// UnmarshalJSON unmarshals either an object with value and provenance or raw value bytes.
func (vf *VectorField[T]) UnmarshalJSON(data []byte) error {
	var obj struct {
		Value      T          `json:"value"`
		Provenance Provenance `json:"provenance"`
	}
	if err := json.Unmarshal(data, &obj); err == nil && obj.Provenance != "" {
		vf.Value = obj.Value
		vf.Provenance = obj.Provenance
		return nil
	}
	var raw struct {
		Value      T          `json:"value"`
		Provenance Provenance `json:"provenance"`
	}
	if err := json.Unmarshal(data, &raw); err == nil {
		vf.Value = raw.Value
		vf.Provenance = raw.Provenance
		return nil
	}
	var val T
	if err := json.Unmarshal(data, &val); err == nil {
		vf.Value = val
		vf.Provenance = ""
		return nil
	}
	return fmt.Errorf("cannot parse vector field from %s", string(data))
}

// LaneCounts tracks unit outcomes in a lane or milestone.
// Offered is the total count of all units offered/attempted across all outcomes
// (qualified + reverted + rejected + abandoned + timed-out + offered).
type LaneCounts struct {
	Qualified int `json:"qualified"`
	Offered   int `json:"offered"` // Total units offered across all dispositions
	Rejected  int `json:"rejected"`
	Abandoned int `json:"abandoned"`
	Reverted  int `json:"reverted"`
	TimedOut  int `json:"timed_out"`
}

// Add counts one unit disposition. Offered is incremented for every unit as the total offered.
func (lc *LaneCounts) Add(disp string) {
	norm := normalizeDisposition(disp)
	lc.Offered++
	switch norm {
	case DispositionQualified:
		lc.Qualified++
	case DispositionReverted:
		lc.Reverted++
	case DispositionRejected:
		lc.Rejected++
	case DispositionAbandoned:
		lc.Abandoned++
	case DispositionTimedOut:
		lc.TimedOut++
	case DispositionOffered:
		// Counted in Offered total
	}
}

// String formats lane counts for human display.
func (lc LaneCounts) String() string {
	parts := []string{
		fmt.Sprintf("%d qualified", lc.Qualified),
	}
	if lc.Reverted > 0 {
		parts = append(parts, fmt.Sprintf("%d reverted", lc.Reverted))
	}
	if lc.Rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d rejected", lc.Rejected))
	}
	if lc.Abandoned > 0 {
		parts = append(parts, fmt.Sprintf("%d abandoned", lc.Abandoned))
	}
	if lc.TimedOut > 0 {
		parts = append(parts, fmt.Sprintf("%d timed-out", lc.TimedOut))
	}
	parts = append(parts, fmt.Sprintf("%d offered", lc.Offered))
	return strings.Join(parts, ", ")
}

// UnitReport represents efficiency metrics for one landed or attempted unit.
type UnitReport struct {
	PullRequestNumber          int                           `json:"pull_request_number"`
	HeadBranch                 string                        `json:"head_branch"`
	Title                      string                        `json:"title"`
	Milestone                  string                        `json:"milestone,omitempty"`
	CreatedAt                  time.Time                     `json:"created_at"`
	MergedAt                   time.Time                     `json:"merged_at"`
	ClosingIssues              []int                         `json:"closing_issues,omitempty"`
	IssueToMerge               string                        `json:"issue_to_merge"`
	IssueToMergeSecs           *int64                        `json:"issue_to_merge_secs,omitempty"`
	FrontierTokens             string                        `json:"frontier_tokens"`
	FrontierTokensNum          *int64                        `json:"frontier_tokens_num,omitempty"`
	Spend                      string                        `json:"spend"`
	SpendAmount                *float64                      `json:"spend_amount,omitempty"`
	OperatorTouches            string                        `json:"operator_touches"`
	OperatorTouchNum           *int                          `json:"operator_touches_num,omitempty"`
	PromptCacheHitRate         string                        `json:"prompt_cache_hit_rate"`
	CacheHitRatio              *float64                      `json:"cache_hit_ratio,omitempty"`
	LocalFirstRatio            string                        `json:"local_first_ratio"`
	LocalRatio                 *float64                      `json:"local_ratio,omitempty"`
	FactHitRatio               string                        `json:"fact_hit_ratio"`
	ChecksBeforeReviews        string                        `json:"checks_before_reviews"`
	Sources                    string                        `json:"sources"`
	TranscriptRequestsNotAdded *int                          `json:"transcript_requests_not_added,omitempty"`
	Disposition                string                        `json:"disposition"`
	Lane                       string                        `json:"lane,omitempty"`
	MetricEpoch                string                        `json:"metric_epoch"`
	TokensByProvider           VectorField[map[string]int64] `json:"tokens_by_provider"`
	WallSeconds                VectorField[int64]            `json:"wall_seconds"`
	ReviewRounds               VectorField[int]              `json:"review_rounds"`
	Retries                    VectorField[int]              `json:"retries"`
	OperatorMinutes            VectorField[float64]          `json:"operator_minutes"`
	EscapedDefects             VectorField[int]              `json:"escaped_defects"`
}

// MilestoneSummary aggregates metrics across units in a milestone.
type MilestoneSummary struct {
	Milestone               string                `json:"milestone,omitempty"`
	UnitsCount              int                   `json:"units_count"`
	QualifiedUnits          int                   `json:"qualified_units"`
	LaneCounts              LaneCounts            `json:"lane_counts"`
	PerLane                 map[string]LaneCounts `json:"per_lane,omitempty"`
	MetricEpoch             string                `json:"metric_epoch"`
	FrontierTokens          string                `json:"frontier_tokens"`
	FrontierTokensNum       *int64                `json:"frontier_tokens_num,omitempty"`
	AttributedSpend         string                `json:"attributed_spend"`
	AttributedSpendNum      *float64              `json:"attributed_spend_num,omitempty"`
	UnattributedSpend       string                `json:"unattributed_spend"`
	UnattributedSpendNum    *float64              `json:"unattributed_spend_num,omitempty"`
	TotalSpend              string                `json:"total_spend"`
	TotalSpendNum           *float64              `json:"total_spend_num,omitempty"`
	OtherUnitsSpend         string                `json:"other_units_spend"`
	OtherUnitsSpendNum      *float64              `json:"other_units_spend_num,omitempty"`
	IssueToMergeUnits       int                   `json:"issue_to_merge_units"`
	AvgIssueToMerge         string                `json:"avg_issue_to_merge"`
	AvgIssueToMergeSecs     *int64                `json:"avg_issue_to_merge_secs,omitempty"`
	OperatorTouches         string                `json:"operator_touches"`
	OperatorTouchNum        *int                  `json:"operator_touches_num,omitempty"`
	PromptCacheHitRate      string                `json:"prompt_cache_hit_rate"`
	CacheHitRatio           *float64              `json:"cache_hit_ratio,omitempty"`
	LocalFirstRatio         string                `json:"local_first_ratio"`
	LocalRatio              *float64              `json:"local_ratio,omitempty"`
	FactHitRatio            string                `json:"fact_hit_ratio"`
	ChecksBeforeReviews     string                `json:"checks_before_reviews"`
	TokensByProvider        map[string]float64    `json:"tokens_by_provider,omitempty"`
	TokensByProviderDisplay string                `json:"tokens_by_provider_display,omitempty"`
	AvgWallSeconds          string                `json:"avg_wall_seconds"`
	AvgWallSecondsNum       *float64              `json:"avg_wall_seconds_num,omitempty"`
	AvgReviewRounds         string                `json:"avg_review_rounds"`
	AvgReviewRoundsNum      *float64              `json:"avg_review_rounds_num,omitempty"`
	AvgRetries              string                `json:"avg_retries"`
	AvgRetriesNum           *float64              `json:"avg_retries_num,omitempty"`
	AvgOperatorMinutes      string                `json:"avg_operator_minutes"`
	AvgOperatorMinutesNum   *float64              `json:"avg_operator_minutes_num,omitempty"`
	EscapedDefects          string                `json:"escaped_defects"`
	EscapedDefectsNum       *int                  `json:"escaped_defects_num,omitempty"`
	EscapedDefectsRate      string                `json:"escaped_defects_rate"`
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

func validateVectorField(prNum int, name string, p Provenance) error {
	if !ValidProvenance(p) {
		return fmt.Errorf("unit #%d: field %q missing or invalid provenance label %q (must be measured, modeled, cited, or interval)", prNum, name, p)
	}
	return nil
}

// ValidateRow checks that a unit row has a valid metric epoch, disposition, and
// required provenance labels on every vector field. A row missing provenance is refused.
func ValidateRow(u UnitReport) error {
	if u.MetricEpoch == "" {
		return fmt.Errorf("unit #%d: missing metric_epoch tag", u.PullRequestNumber)
	}
	if u.MetricEpoch != CurrentMetricEpoch {
		return fmt.Errorf("unit #%d: incompatible metric epoch %q (expected %q)", u.PullRequestNumber, u.MetricEpoch, CurrentMetricEpoch)
	}
	if !ValidDisposition(u.Disposition) {
		return fmt.Errorf("unit #%d: invalid disposition %q", u.PullRequestNumber, u.Disposition)
	}
	if err := validateVectorField(u.PullRequestNumber, "tokens_by_provider", u.TokensByProvider.Provenance); err != nil {
		return err
	}
	if err := validateVectorField(u.PullRequestNumber, "wall_seconds", u.WallSeconds.Provenance); err != nil {
		return err
	}
	if err := validateVectorField(u.PullRequestNumber, "review_rounds", u.ReviewRounds.Provenance); err != nil {
		return err
	}
	if err := validateVectorField(u.PullRequestNumber, "retries", u.Retries.Provenance); err != nil {
		return err
	}
	if err := validateVectorField(u.PullRequestNumber, "operator_minutes", u.OperatorMinutes.Provenance); err != nil {
		return err
	}
	if err := validateVectorField(u.PullRequestNumber, "escaped_defects", u.EscapedDefects.Provenance); err != nil {
		return err
	}
	return nil
}

// ValidateRows validates every row and ensures metric epochs are not mixed.
func ValidateRows(units []UnitReport) error {
	var firstEpoch string
	for i, u := range units {
		if i == 0 {
			firstEpoch = u.MetricEpoch
		} else if u.MetricEpoch != firstEpoch {
			return fmt.Errorf("mixed metric epochs in ledger: found %q and %q (schema change must not silently mix old and new rows)", firstEpoch, u.MetricEpoch)
		}
	}
	for _, u := range units {
		if err := ValidateRow(u); err != nil {
			return err
		}
	}
	return nil
}

// FormatZeroFailureClaim prints n and the rule-of-three 95% upper bound.
func FormatZeroFailureClaim(n int) string {
	if n <= 0 {
		return UndefinedRate
	}
	bound := 3.0 / float64(n)
	return fmt.Sprintf("0 (n=%d, rule-of-three bound <= %.2f)", n, bound)
}
