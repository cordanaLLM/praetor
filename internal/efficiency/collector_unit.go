// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"fmt"
	"strings"
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

func unmarshalVectorValue[T any](raw *forge.VectorFieldRaw, target *VectorField[T]) {
	if raw == nil {
		return
	}
	var val T
	if len(raw.Value) > 0 {
		if err := json.Unmarshal(raw.Value, &val); err == nil {
			target.Value = val
		}
	}
	target.Provenance = Provenance(raw.Provenance)
}

func initUnitVectorFields(pr forge.MergedPullRequest, itmSecs *int64, unit *UnitReport) {
	unit.TokensByProvider = VectorField[map[string]int64]{Value: make(map[string]int64), Provenance: ProvenanceMeasured}
	unit.WallSeconds = VectorField[int64]{Provenance: ProvenanceMeasured}
	if itmSecs != nil {
		unit.WallSeconds.Value = *itmSecs
	}
	unit.ReviewRounds = VectorField[int]{Value: 1, Provenance: ProvenanceMeasured}
	unit.Retries = VectorField[int]{Provenance: ProvenanceMeasured}
	unit.OperatorMinutes = VectorField[float64]{Provenance: ProvenanceMeasured}
	unit.EscapedDefects = VectorField[int]{Provenance: ProvenanceMeasured}

	unmarshalVectorValue(pr.TokensByProvider, &unit.TokensByProvider)
	unmarshalVectorValue(pr.WallSeconds, &unit.WallSeconds)
	unmarshalVectorValue(pr.ReviewRounds, &unit.ReviewRounds)
	unmarshalVectorValue(pr.Retries, &unit.Retries)
	unmarshalVectorValue(pr.OperatorMinutes, &unit.OperatorMinutes)
	unmarshalVectorValue(pr.EscapedDefects, &unit.EscapedDefects)
}

func resolveUnitDisposition(pr forge.MergedPullRequest) string {
	if pr.Disposition != "" {
		return normalizeDisposition(pr.Disposition)
	}
	lowerTitle := strings.ToLower(pr.Title)
	if strings.HasPrefix(lowerTitle, "revert ") || strings.HasPrefix(lowerTitle, "revert:") {
		return DispositionReverted
	}
	return DispositionQualified
}

func newUnit(pr forge.MergedPullRequest) UnitReport {
	closing := make([]int, 0, len(pr.ClosingIssues))
	for _, ci := range pr.ClosingIssues {
		closing = append(closing, ci.Number)
	}
	itm, secs := calculateIssueToMerge(pr)
	disp := resolveUnitDisposition(pr)
	epoch := pr.MetricEpoch
	if epoch == "" {
		epoch = CurrentMetricEpoch
	}

	unit := UnitReport{
		PullRequestNumber:   pr.EffectiveNumber(),
		HeadBranch:          pr.HeadBranch,
		Title:               pr.Title,
		Milestone:           pr.Milestone,
		CreatedAt:           pr.CreatedAt,
		MergedAt:            pr.MergedAt,
		ClosingIssues:       closing,
		IssueToMerge:        itm,
		IssueToMergeSecs:    secs,
		Disposition:         disp,
		Lane:                pr.Lane,
		MetricEpoch:         epoch,
		FrontierTokens:      NotMeasured,
		Spend:               NotMeasured,
		OperatorTouches:     NotMeasured,
		PromptCacheHitRate:  NotMeasured,
		LocalFirstRatio:     NotMeasured,
		FactHitRatio:        FollowUpRefs,
		ChecksBeforeReviews: FollowUpRefs,
		Sources:             NotMeasured,
	}
	initUnitVectorFields(pr, secs, &unit)
	return unit
}

// unitUsage is the request and frontier-token count of one unit and where it came from.
type unitUsage struct {
	requests                  int
	localRequests             int
	frontierTokens            int64
	measured                  bool
	sources                   string
	omittedTranscriptRequests int
}

func combinedUsage(prNum int, stats *BranchTranscriptStats, spend *SpendReport, viaGateway bool) unitUsage {
	if viaGateway {
		u := gatewayUsage(prNum, spend)
		u.omittedTranscriptRequests = stats.TotalRequests
		return u
	}
	return unitUsage{
		requests:       spend.RequestsByPRNumber[prNum] + stats.TotalRequests,
		localRequests:  spend.LocalRequestsByPRNumber[prNum] + stats.LocalRequests,
		frontierTokens: spend.FrontierTokensByPRNumber[prNum] + stats.FrontierTokens,
		measured:       true,
		sources:        "transcripts+gateway",
	}
}

func gatewayUsage(prNum int, spend *SpendReport) unitUsage {
	return unitUsage{
		requests:       spend.RequestsByPRNumber[prNum],
		localRequests:  spend.LocalRequestsByPRNumber[prNum],
		frontierTokens: spend.FrontierTokensByPRNumber[prNum],
		measured:       true,
		sources:        "gateway",
	}
}

func transcriptUsage(stats *BranchTranscriptStats) unitUsage {
	return unitUsage{
		requests:       stats.TotalRequests,
		localRequests:  stats.LocalRequests,
		frontierTokens: stats.FrontierTokens,
		measured:       true,
		sources:        "transcripts",
	}
}

// usageFor determines requests, local-first ratio and frontier tokens according to
// transcripts_via_gateway. When false (default), transcript requests and gateway entries
// both count in full. When true, gateway entries count and transcript usage for joined
// units is not added, with unadded transcript requests reported.
func usageFor(prNum int, stats *BranchTranscriptStats, spend *SpendReport, viaGateway bool) unitUsage {
	hasGateway := spend != nil && spend.RequestsByPRNumber[prNum] > 0
	hasTranscripts := stats != nil && stats.TotalRequests > 0

	if hasGateway && hasTranscripts {
		return combinedUsage(prNum, stats, spend, viaGateway)
	}
	if hasGateway {
		return gatewayUsage(prNum, spend)
	}
	if hasTranscripts {
		return transcriptUsage(stats)
	}
	return unitUsage{sources: NotMeasured}
}

func (u unitUsage) apply(unit *UnitReport) {
	unit.Sources = u.sources
	if u.omittedTranscriptRequests > 0 {
		omitted := u.omittedTranscriptRequests
		unit.TranscriptRequestsNotAdded = &omitted
	}
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

func (c *Collector) buildUnitReport(pr forge.MergedPullRequest, owners map[string]int, transStats map[string]*BranchTranscriptStats, spend *SpendReport, sources SourcesMeasured, report *Report) UnitReport {
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
	usage := usageFor(pr.Number, stats, spend, c.transcriptsViaGateway())
	usage.apply(&unit)
	if usage.omittedTranscriptRequests > 0 && report != nil {
		report.Notes = append(report.Notes, fmt.Sprintf("unit #%d: %d transcript requests not added (transcripts_via_gateway=true)", pr.Number, usage.omittedTranscriptRequests))
	}
	return unit
}
