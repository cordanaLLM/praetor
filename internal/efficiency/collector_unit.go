// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func earliestClosingIssueCreatedAt(issues []forge.ClosingIssue) time.Time {
	var earliest time.Time
	for _, ci := range issues {
		if ci.CreatedAt.IsZero() {
			continue
		}
		if earliest.IsZero() || ci.CreatedAt.Before(earliest) {
			earliest = ci.CreatedAt
		}
	}
	return earliest
}

// calculateIssueToMerge measures from the earliest closing issue's creation to the merge. A
// unit without a closing issue creation time is not measured; the pull request's own age is
// a different metric and is never substituted.
func calculateIssueToMerge(pr forge.MergedPullRequest) (string, *int64) {
	created := earliestClosingIssueCreatedAt(pr.ClosingIssues)
	if pr.MergedAt.IsZero() || created.IsZero() {
		return NotMeasured, nil
	}
	dur := pr.MergedAt.Sub(created)
	if dur < 0 {
		dur = 0
	}
	secs := int64(dur.Seconds())
	return formatDuration(dur), &secs
}

func newUnit(pr forge.MergedPullRequest) UnitReport {
	closing := make([]int, 0, len(pr.ClosingIssues))
	for _, ci := range pr.ClosingIssues {
		closing = append(closing, ci.Number)
	}
	itm, secs := calculateIssueToMerge(pr)
	return UnitReport{
		PullRequestNumber:   pr.Number,
		HeadBranch:          pr.HeadBranch,
		Title:               pr.Title,
		Milestone:           pr.Milestone,
		CreatedAt:           pr.CreatedAt,
		MergedAt:            pr.MergedAt,
		ClosingIssues:       closing,
		IssueToMerge:        itm,
		IssueToMergeSecs:    secs,
		FrontierTokens:      NotMeasured,
		Spend:               NotMeasured,
		OperatorTouches:     NotMeasured,
		PromptCacheHitRate:  NotMeasured,
		LocalFirstRatio:     NotMeasured,
		FactHitRatio:        FollowUpRefs,
		ChecksBeforeReviews: FollowUpRefs,
	}
}

// unitUsage is the request and frontier-token count of one unit and where it came from.
type unitUsage struct {
	requests       int
	localRequests  int
	frontierTokens int64
	measured       bool
}

// usageFor picks one source for requests, local-first and frontier tokens: gateway entries
// attributed to the pull request when there are any (they hold local models and cover every
// request), else the joined transcripts. The two never add, because agent requests that go
// through the gateway appear in both.
func usageFor(prNum int, stats *BranchTranscriptStats, spend *SpendReport) unitUsage {
	if spend != nil && spend.RequestsByPRNumber[prNum] > 0 {
		return unitUsage{
			requests:       spend.RequestsByPRNumber[prNum],
			localRequests:  spend.LocalRequestsByPRNumber[prNum],
			frontierTokens: spend.FrontierTokensByPRNumber[prNum],
			measured:       true,
		}
	}
	if stats != nil && stats.TotalRequests > 0 {
		return unitUsage{requests: stats.TotalRequests, localRequests: stats.LocalRequests, frontierTokens: stats.FrontierTokens, measured: true}
	}
	return unitUsage{}
}

func (u unitUsage) apply(unit *UnitReport) {
	if !u.measured {
		return
	}
	unit.FrontierTokens = formatTokens(u.frontierTokens)
	tokens := u.frontierTokens
	unit.FrontierTokensNum = &tokens
	ratio := float64(u.localRequests) / float64(u.requests)
	unit.LocalFirstRatio = formatPercent(ratio)
	unit.LocalRatio = &ratio
}

func (c *Collector) buildUnitReport(pr forge.MergedPullRequest, owners map[string]int, transStats map[string]*BranchTranscriptStats, spend *SpendReport, sources SourcesMeasured) UnitReport {
	unit := newUnit(pr)
	var stats *BranchTranscriptStats
	// A reused branch name belongs to the pull request merged last; the others get no transcript join.
	if sources.Transcripts && owners[pr.HeadBranch] == pr.Number {
		stats = transStats[pr.HeadBranch]
	}
	if stats != nil {
		touches := stats.OperatorTouches
		unit.OperatorTouches = fmt.Sprintf("%d", touches)
		unit.OperatorTouchNum = &touches
		if rate, ok := stats.PromptCacheHitRate(); ok {
			unit.PromptCacheHitRate = formatPercent(rate)
			unit.CacheHitRatio = &rate
		}
	}
	if spend != nil && spend.RequestsByPRNumber[pr.Number] > 0 {
		amount := spend.SpendByPRNumber[pr.Number]
		unit.Spend = formatSpend(amount)
		unit.SpendAmount = &amount
	}
	usageFor(pr.Number, stats, spend).apply(&unit)
	return unit
}
