// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/router"
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

// Dispositions for unit outcomes. A records row names one of these exact spellings; there are
// no aliases.
const (
	DispositionQualified = "qualified"
	DispositionOffered   = "offered"
	DispositionRejected  = "rejected"
	DispositionAbandoned = "abandoned"
	DispositionReverted  = "reverted"
	DispositionTimedOut  = "timed_out"
)

// ValidDisposition checks if disp is a recognized unit outcome disposition, spelled exactly.
func ValidDisposition(disp string) bool {
	switch disp {
	case DispositionQualified, DispositionOffered, DispositionRejected, DispositionAbandoned, DispositionReverted, DispositionTimedOut:
		return true
	default:
		return false
	}
}

// VectorField is one measured vector value with its required provenance label. An interval
// field also carries Low and High, the bounds around Value; no other label carries bounds. A
// field that no source measured is a nil *VectorField, printed as null.
type VectorField[T any] struct {
	Value      T          `json:"value"`
	Provenance Provenance `json:"provenance"`
	Low        *T         `json:"low,omitempty"`
	High       *T         `json:"high,omitempty"`
}

// LaneCounts tracks unit outcomes in a lane or milestone: one count per disposition, and Total
// over every unit whatever its disposition.
type LaneCounts struct {
	Qualified int `json:"qualified"`
	Offered   int `json:"offered"`
	Rejected  int `json:"rejected"`
	Abandoned int `json:"abandoned"`
	Reverted  int `json:"reverted"`
	TimedOut  int `json:"timed_out"`
	Total     int `json:"total"`
}

// Add counts one unit under its disposition and in Total.
func (lc *LaneCounts) Add(disp string) {
	lc.Total++
	switch disp {
	case DispositionQualified:
		lc.Qualified++
	case DispositionOffered:
		lc.Offered++
	case DispositionReverted:
		lc.Reverted++
	case DispositionRejected:
		lc.Rejected++
	case DispositionAbandoned:
		lc.Abandoned++
	case DispositionTimedOut:
		lc.TimedOut++
	}
}

// String formats lane counts for human display: the qualified count, every other disposition
// that occurs, and the total.
func (lc LaneCounts) String() string {
	parts := []string{fmt.Sprintf("%d %s", lc.Qualified, DispositionQualified)}
	for _, c := range []struct {
		n    int
		disp string
	}{{lc.Reverted, DispositionReverted}, {lc.Rejected, DispositionRejected}, {lc.Abandoned, DispositionAbandoned}, {lc.TimedOut, DispositionTimedOut}, {lc.Offered, DispositionOffered}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.disp))
		}
	}
	parts = append(parts, fmt.Sprintf("%d total", lc.Total))
	return strings.Join(parts, ", ")
}

// UnitReport represents efficiency metrics for one landed or attempted unit. The *Num usage
// counts are the parts of the unit's local-first and prompt-cache ratios, so the summary can
// pool them over all usage.
type UnitReport struct {
	PullRequestNumber          int                            `json:"pull_request_number"`
	HeadBranch                 string                         `json:"head_branch"`
	Title                      string                         `json:"title"`
	Milestone                  string                         `json:"milestone,omitempty"`
	CreatedAt                  time.Time                      `json:"created_at"`
	MergedAt                   time.Time                      `json:"merged_at"`
	ClosingIssues              []int                          `json:"closing_issues,omitempty"`
	IssueToMerge               string                         `json:"issue_to_merge"`
	IssueToMergeSecs           *int64                         `json:"issue_to_merge_secs,omitempty"`
	FrontierTokens             string                         `json:"frontier_tokens"`
	FrontierTokensNum          *int64                         `json:"frontier_tokens_num,omitempty"`
	Spend                      string                         `json:"spend"`
	SpendAmount                *float64                       `json:"spend_amount,omitempty"`
	OperatorTouches            string                         `json:"operator_touches"`
	OperatorTouchNum           *int                           `json:"operator_touches_num,omitempty"`
	PromptCacheHitRate         string                         `json:"prompt_cache_hit_rate"`
	CacheHitRatio              *float64                       `json:"cache_hit_ratio,omitempty"`
	CacheReadTokensNum         *int64                         `json:"cache_read_tokens_num,omitempty"`
	PromptInputTokensNum       *int64                         `json:"prompt_input_tokens_num,omitempty"`
	LocalFirstRatio            string                         `json:"local_first_ratio"`
	LocalRatio                 *float64                       `json:"local_ratio,omitempty"`
	RequestsNum                *int                           `json:"requests_num,omitempty"`
	LocalRequestsNum           *int                           `json:"local_requests_num,omitempty"`
	FactHitRatio               string                         `json:"fact_hit_ratio"`
	ChecksBeforeReviews        string                         `json:"checks_before_reviews"`
	Sources                    string                         `json:"sources"`
	TranscriptRequestsNotAdded *int                           `json:"transcript_requests_not_added,omitempty"`
	Disposition                string                         `json:"disposition"`
	Lane                       string                         `json:"lane,omitempty"`
	MetricEpoch                string                         `json:"metric_epoch"`
	TokensByProvider           *VectorField[map[string]int64] `json:"tokens_by_provider"`
	WallSeconds                *VectorField[int64]            `json:"wall_seconds"`
	ReviewRounds               *VectorField[int]              `json:"review_rounds"`
	Retries                    *VectorField[int]              `json:"retries"`
	OperatorMinutes            *VectorField[float64]          `json:"operator_minutes"`
	EscapedDefects             *VectorField[int]              `json:"escaped_defects"`
	ResolvedModel              string                         `json:"resolved_model,omitempty"`
	Identity                   *router.RunIdentity            `json:"identity,omitempty"`
	IdentityKey                string                         `json:"identity_key,omitempty"`
	EstimateError              string                         `json:"estimate_error,omitempty"`
	EstimateErrorAmount        *float64                       `json:"estimate_error_amount,omitempty"`
}

// ProvenanceMix counts the units behind one summary figure per provenance label, so a figure
// that adds measured and modeled values says so.
type ProvenanceMix struct {
	Measured int `json:"measured"`
	Modeled  int `json:"modeled"`
	Cited    int `json:"cited"`
	Interval int `json:"interval"`
}

func (m *ProvenanceMix) add(p Provenance) {
	switch p {
	case ProvenanceMeasured:
		m.Measured++
	case ProvenanceModeled:
		m.Modeled++
	case ProvenanceCited:
		m.Cited++
	case ProvenanceInterval:
		m.Interval++
	}
}

// String lists the labels that occur, for example "measured 2, modeled 1".
func (m ProvenanceMix) String() string {
	parts := make([]string, 0, 4)
	for _, c := range []struct {
		label Provenance
		n     int
	}{{ProvenanceMeasured, m.Measured}, {ProvenanceModeled, m.Modeled}, {ProvenanceCited, m.Cited}, {ProvenanceInterval, m.Interval}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", c.label, c.n))
		}
	}
	return strings.Join(parts, ", ")
}

// Interval is a closed range of a summary figure.
type Interval struct {
	Low  float64 `json:"low"`
	High float64 `json:"high"`
}

// VectorComponent is one number of a vector summary: the field itself for a scalar, one
// provider for tokens_by_provider. Total runs over every unit that carries the field, failed
// dispositions included; PerQualifiedUnit is Total over the qualified-unit count and is nil
// when no unit qualified. The bounds are set when an interval field contributed.
type VectorComponent struct {
	Key                    string    `json:"key,omitempty"`
	Total                  float64   `json:"total"`
	TotalBounds            *Interval `json:"total_bounds,omitempty"`
	PerQualifiedUnit       *float64  `json:"per_qualified_unit,omitempty"`
	PerQualifiedUnitBounds *Interval `json:"per_qualified_unit_bounds,omitempty"`
}

// VectorSummary is one vector field over the selected units: cost per qualified unit, the
// provenance mix of the units behind it, and LowerBound when some units did not measure the
// field, so the total only bounds the cost from below.
type VectorSummary struct {
	Display       string            `json:"display"`
	UnitsMeasured int               `json:"units_measured"`
	LowerBound    bool              `json:"lower_bound,omitempty"`
	Provenance    ProvenanceMix     `json:"provenance"`
	Components    []VectorComponent `json:"components,omitempty"`
}

// MilestoneSummary aggregates metrics across units in a milestone under three rules, named in
// every display string:
//   - resource consumption (frontier tokens, operator touches, attributed spend and the vector
//     fields) is the total over every unit, failed dispositions included, divided by the
//     qualified-unit count;
//   - latency (issue-to-merge) is the mean over qualified units;
//   - ratios (prompt-cache hit rate, local-first ratio) are pooled over the usage of every unit.
type MilestoneSummary struct {
	Milestone                      string                `json:"milestone,omitempty"`
	UnitsCount                     int                   `json:"units_count"`
	QualifiedUnits                 int                   `json:"qualified_units"`
	LaneCounts                     LaneCounts            `json:"lane_counts"`
	PerLane                        map[string]LaneCounts `json:"per_lane,omitempty"`
	MetricEpoch                    string                `json:"metric_epoch"`
	FrontierTokens                 string                `json:"frontier_tokens"`
	FrontierTokensNum              *int64                `json:"frontier_tokens_num,omitempty"`
	FrontierTokensPerQualifiedUnit *float64              `json:"frontier_tokens_per_qualified_unit,omitempty"`
	AttributedSpend                string                `json:"attributed_spend"`
	AttributedSpendNum             *float64              `json:"attributed_spend_num,omitempty"`
	SpendPerQualifiedUnit          string                `json:"spend_per_qualified_unit"`
	SpendPerQualifiedUnitNum       *float64              `json:"spend_per_qualified_unit_num,omitempty"`
	UnattributedSpend              string                `json:"unattributed_spend"`
	UnattributedSpendNum           *float64              `json:"unattributed_spend_num,omitempty"`
	TotalSpend                     string                `json:"total_spend"`
	TotalSpendNum                  *float64              `json:"total_spend_num,omitempty"`
	OtherUnitsSpend                string                `json:"other_units_spend"`
	OtherUnitsSpendNum             *float64              `json:"other_units_spend_num,omitempty"`
	IssueToMergeUnits              int                   `json:"issue_to_merge_units"`
	AvgIssueToMerge                string                `json:"avg_issue_to_merge"`
	AvgIssueToMergeSecs            *int64                `json:"avg_issue_to_merge_secs,omitempty"`
	OperatorTouches                string                `json:"operator_touches"`
	OperatorTouchNum               *int                  `json:"operator_touches_num,omitempty"`
	OperatorTouchesPerQualified    *float64              `json:"operator_touches_per_qualified_unit,omitempty"`
	PromptCacheHitRate             string                `json:"prompt_cache_hit_rate"`
	CacheHitRatio                  *float64              `json:"cache_hit_ratio,omitempty"`
	LocalFirstRatio                string                `json:"local_first_ratio"`
	LocalRatio                     *float64              `json:"local_ratio,omitempty"`
	FactHitRatio                   string                `json:"fact_hit_ratio"`
	ChecksBeforeReviews            string                `json:"checks_before_reviews"`
	TokensByProvider               VectorSummary         `json:"tokens_by_provider"`
	WallSeconds                    VectorSummary         `json:"wall_seconds"`
	ReviewRounds                   VectorSummary         `json:"review_rounds"`
	Retries                        VectorSummary         `json:"retries"`
	OperatorMinutes                VectorSummary         `json:"operator_minutes"`
	EscapedDefects                 VectorSummary         `json:"escaped_defects"`
	EstimateError                  string                `json:"estimate_error,omitempty"`
	EstimateErrorAmount            *float64              `json:"estimate_error_amount,omitempty"`
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

// FormatZeroFailureClaim words a failure count of zero per qualified unit: with no failure in
// the observed units, the rule of three bounds the expected failures at 3, so at most 3/qualified
// per qualified unit (95 %). It is undefined without a qualified unit.
func FormatZeroFailureClaim(qualified, observed int) string {
	if qualified <= 0 {
		return UndefinedRate
	}
	return fmt.Sprintf("0 (rule-of-three bound <= %.2f per qualified unit; n=%d qualified units, %d units observed)", 3.0/float64(qualified), qualified, observed)
}
