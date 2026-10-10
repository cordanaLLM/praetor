// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
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

// recordVectorFields reads the six vector fields of a records row through parseVectorField.
func recordVectorFields(pr forge.MergedPullRequest, unit *UnitReport) error {
	var errs [len(vectorFieldNames)]error
	unit.TokensByProvider, errs[0] = parseVectorField[map[string]int64](pr.TokensByProvider)
	unit.WallSeconds, errs[1] = parseVectorField[int64](pr.WallSeconds)
	unit.ReviewRounds, errs[2] = parseVectorField[int](pr.ReviewRounds)
	unit.Retries, errs[3] = parseVectorField[int](pr.Retries)
	unit.OperatorMinutes, errs[4] = parseVectorField[float64](pr.OperatorMinutes)
	unit.EscapedDefects, errs[5] = parseVectorField[int](pr.EscapedDefects)
	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("field %q: %w", vectorFieldNames[i], err)
		}
	}
	return nil
}

// LiveUnmeasuredFields names the vector fields a live forge listing has no source for, with
// the reason printed under Notes.
const LiveUnmeasuredFields = "live forge: tokens_by_provider, review_rounds, retries, operator_minutes and escaped_defects are not measured (the merged pull request listing carries no per-provider token, review round, retry, operator time or escaped defect data; pass --forge-records with labelled values to report them)"

// LiveDispositionNote states how a live listing row gets its disposition.
const LiveDispositionNote = "live forge: a merged pull request counts as qualified unless a later merged revert names its title; check and review results are not read"

// stampLiveUnit fills the ledger fields of a live listing row, which carries none: the current
// metric epoch, since the collector builds the row under the current schema, and wall_seconds
// measured from the forge's creation and merge timestamps. Every other vector field stays nil
// (not measured); LiveUnmeasuredFields says why.
func stampLiveUnit(pr forge.MergedPullRequest, unit *UnitReport) {
	unit.MetricEpoch = CurrentMetricEpoch
	if pr.CreatedAt.IsZero() || pr.MergedAt.IsZero() || pr.MergedAt.Before(pr.CreatedAt) {
		return
	}
	unit.WallSeconds = &VectorField[int64]{Value: int64(pr.MergedAt.Sub(pr.CreatedAt).Seconds()), Provenance: ProvenanceMeasured}
}

func resolveUnitDisposition(pr forge.MergedPullRequest) string {
	if pr.Disposition != "" {
		return normalizeDisposition(pr.Disposition)
	}
	return DispositionQualified
}

func targetRevertedTitle(title string) string {
	t := strings.TrimSpace(title)
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "revert \"") {
		rest := t[8:]
		if idx := strings.LastIndex(rest, "\""); idx != -1 {
			return strings.TrimSpace(rest[:idx])
		}
		return strings.TrimSpace(rest)
	}
	if strings.HasPrefix(lower, "revert: ") {
		return strings.TrimSpace(t[8:])
	}
	if strings.HasPrefix(lower, "revert ") {
		return strings.TrimSpace(t[7:])
	}
	return ""
}

func applyRevertDispositions(units []UnitReport) {
	for i := range units {
		revertTitle := targetRevertedTitle(units[i].Title)
		if revertTitle == "" {
			continue
		}
		for j := range units {
			if i == j {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(units[j].Title), revertTitle) {
				units[j].Disposition = DispositionReverted
				break
			}
		}
	}
}

// newUnit builds the row of one pull request. A records row must carry its own epoch and every
// vector field; a live listing row is stamped by stampLiveUnit.
func newUnit(pr forge.MergedPullRequest, live bool) (UnitReport, error) {
	closing := make([]int, 0, len(pr.ClosingIssues))
	for _, ci := range pr.ClosingIssues {
		closing = append(closing, ci.Number)
	}
	itm, secs := calculateIssueToMerge(pr)
	disp := resolveUnitDisposition(pr)

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
		MetricEpoch:         pr.MetricEpoch,
		FrontierTokens:      NotMeasured,
		Spend:               NotMeasured,
		OperatorTouches:     NotMeasured,
		PromptCacheHitRate:  NotMeasured,
		LocalFirstRatio:     NotMeasured,
		FactHitRatio:        FollowUpRefs,
		ChecksBeforeReviews: FollowUpRefs,
		Sources:             NotMeasured,
	}
	if live {
		stampLiveUnit(pr, &unit)
		return unit, nil
	}
	if err := recordVectorFields(pr, &unit); err != nil {
		return unit, fmt.Errorf("unit #%d: %w", pr.EffectiveNumber(), err)
	}
	return unit, nil
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
	requests, local := u.requests, u.localRequests
	unit.RequestsNum, unit.LocalRequestsNum = &requests, &local
	ratio := float64(u.localRequests) / float64(u.requests)
	unit.LocalFirstRatio = formatPercent(ratio)
	unit.LocalRatio = &ratio
}

// unitSources is what every unit of one run joins against.
type unitSources struct {
	owners     map[string]int
	transcript map[string]*BranchTranscriptStats
	spend      *SpendReport
	measured   SourcesMeasured
	live       bool
}

func applyTranscriptStats(stats *BranchTranscriptStats, unit *UnitReport) {
	touches := stats.OperatorTouches
	unit.OperatorTouches = fmt.Sprintf("%d", touches)
	unit.OperatorTouchNum = &touches
	if rate, ok := stats.PromptCacheHitRate(); ok {
		unit.PromptCacheHitRate = formatPercent(rate)
		unit.CacheHitRatio = &rate
		read := stats.CacheReadTokens
		input := stats.InputTokens + stats.CacheCreationTokens + stats.CacheReadTokens
		unit.CacheReadTokensNum, unit.PromptInputTokensNum = &read, &input
	}
}

func (c *Collector) buildUnitReport(pr forge.MergedPullRequest, src unitSources, report *Report) (UnitReport, error) {
	unit, err := newUnit(pr, src.live)
	if err != nil {
		return unit, err
	}
	var stats *BranchTranscriptStats
	// A reused branch name belongs to the pull request merged last; the others get no transcript join.
	if src.measured.Transcripts && src.owners[pr.HeadBranch] == pr.Number {
		stats = src.transcript[pr.HeadBranch]
	}
	if stats != nil {
		applyTranscriptStats(stats, &unit)
	}
	spend := src.spend
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
	return unit, nil
}
