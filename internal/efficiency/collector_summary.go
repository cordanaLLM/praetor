// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// buildMilestoneSummary aggregates the units. Every average and ratio runs over the units that
// have the metric and says how many; a zero denominator prints undefined.
func (c *Collector) buildMilestoneSummary(report *Report, spend *SpendReport) {
	summary := &report.MilestoneSummary
	summary.UnitsCount = len(report.Units)
	summary.MetricEpoch = CurrentMetricEpoch

	for _, u := range report.Units {
		summary.LaneCounts.Add(u.Disposition)
	}
	summary.QualifiedUnits = summary.LaneCounts.Qualified

	if summary.QualifiedUnits == 0 {
		applyZeroQualifiedSummary(summary)
		return
	}

	summarizeIssueToMerge(report.Units, summary)
	summarizeTranscriptMetrics(report.Units, summary)
	summarizeGatewayAndTranscriptUsage(report.Units, summary)
	summarizeVectorMetrics(report.Units, summary, summary.QualifiedUnits)
	if report.Sources.SpendLog && spend != nil {
		summarizeSpend(report.Units, spend, summary)
	}
}

func applyZeroQualifiedSummary(summary *MilestoneSummary) {
	summary.AvgIssueToMerge = UndefinedRate
	summary.AvgWallSeconds = UndefinedRate
	summary.AvgReviewRounds = UndefinedRate
	summary.AvgRetries = UndefinedRate
	summary.AvgOperatorMinutes = UndefinedRate
	summary.EscapedDefects = UndefinedRate
	summary.EscapedDefectsRate = UndefinedRate
	summary.OperatorTouches = UndefinedRate
	summary.FrontierTokens = UndefinedRate
	summary.PromptCacheHitRate = UndefinedRate
	summary.LocalFirstRatio = UndefinedRate
	summary.FactHitRatio = UndefinedRate
	summary.ChecksBeforeReviews = UndefinedRate
	summary.TokensByProviderDisplay = UndefinedRate
}

func summarizeVectorMetrics(units []UnitReport, summary *MilestoneSummary, qualified int) {
	summarizeWallSeconds(units, summary, qualified)
	summarizeReviewRounds(units, summary, qualified)
	summarizeRetries(units, summary, qualified)
	summarizeOperatorMinutes(units, summary, qualified)
	summarizeEscapedDefects(units, summary, qualified)
	summarizeTokensByProvider(units, summary, qualified)
}

func summarizeWallSeconds(units []UnitReport, summary *MilestoneSummary, qualified int) {
	var total int64
	for _, u := range units {
		total += u.WallSeconds.Value
	}
	avg := float64(total) / float64(qualified)
	summary.AvgWallSecondsNum = &avg
	summary.AvgWallSeconds = formatDuration(time.Duration(avg) * time.Second)
}

func summarizeReviewRounds(units []UnitReport, summary *MilestoneSummary, qualified int) {
	var total int
	for _, u := range units {
		total += u.ReviewRounds.Value
	}
	avg := float64(total) / float64(qualified)
	summary.AvgReviewRoundsNum = &avg
	summary.AvgReviewRounds = fmt.Sprintf("%.2f per qualified unit", avg)
}

func summarizeRetries(units []UnitReport, summary *MilestoneSummary, qualified int) {
	var total int
	for _, u := range units {
		total += u.Retries.Value
	}
	avg := float64(total) / float64(qualified)
	summary.AvgRetriesNum = &avg
	summary.AvgRetries = fmt.Sprintf("%.2f per qualified unit", avg)
}

func summarizeOperatorMinutes(units []UnitReport, summary *MilestoneSummary, qualified int) {
	var total float64
	for _, u := range units {
		total += u.OperatorMinutes.Value
	}
	avg := total / float64(qualified)
	summary.AvgOperatorMinutesNum = &avg
	summary.AvgOperatorMinutes = fmt.Sprintf("%.1fm per qualified unit", avg)
}

func summarizeEscapedDefects(units []UnitReport, summary *MilestoneSummary, qualified int) {
	var total int
	for _, u := range units {
		total += u.EscapedDefects.Value
	}
	summary.EscapedDefectsNum = &total
	if total == 0 {
		claim := FormatZeroFailureClaim(qualified)
		summary.EscapedDefectsRate = claim
		summary.EscapedDefects = claim
		return
	}
	rate := float64(total) / float64(qualified)
	summary.EscapedDefectsRate = fmt.Sprintf("%.2f per qualified unit", rate)
	summary.EscapedDefects = fmt.Sprintf("%d (%.2f per qualified unit)", total, rate)
}

func summarizeTokensByProvider(units []UnitReport, summary *MilestoneSummary, qualified int) {
	totals := make(map[string]int64)
	for _, u := range units {
		for prov, count := range u.TokensByProvider.Value {
			totals[prov] += count
		}
	}
	if len(totals) == 0 {
		return
	}
	rates := make(map[string]float64, len(totals))
	keys := make([]string, 0, len(totals))
	for prov, count := range totals {
		rates[prov] = float64(count) / float64(qualified)
		keys = append(keys, prov)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %.1f/unit", k, rates[k]))
	}
	summary.TokensByProvider = rates
	summary.TokensByProviderDisplay = strings.Join(parts, ", ")
}

func summarizeIssueToMerge(units []UnitReport, summary *MilestoneSummary) {
	var total int64
	measured := 0
	for _, u := range units {
		if u.IssueToMergeSecs != nil {
			total += *u.IssueToMergeSecs
			measured++
		}
	}
	summary.IssueToMergeUnits = measured
	if measured == 0 {
		summary.AvgIssueToMerge = NotMeasured
		return
	}
	avg := total / int64(measured)
	summary.AvgIssueToMergeSecs = &avg
	summary.AvgIssueToMerge = fmt.Sprintf("%s (%d of %d units measured)", formatDuration(time.Duration(avg)*time.Second), measured, len(units))
}

func summarizeTranscriptMetrics(units []UnitReport, summary *MilestoneSummary) {
	touches, measured := 0, 0
	for _, u := range units {
		if u.OperatorTouchNum != nil {
			touches += *u.OperatorTouchNum
			measured++
		}
	}
	if measured > 0 {
		summary.OperatorTouches = fmt.Sprintf("%d (%d of %d units measured)", touches, measured, len(units))
		summary.OperatorTouchNum = &touches
	} else {
		summary.OperatorTouches = NotMeasured
	}
}

// summarizeGatewayAndTranscriptUsage sums frontier tokens and pools the local-first ratio
// over the units that have request counts.
func summarizeGatewayAndTranscriptUsage(units []UnitReport, summary *MilestoneSummary) {
	var frontier int64
	var tokenUnits int
	var localSum, ratioUnits float64
	for _, u := range units {
		if u.FrontierTokensNum != nil {
			frontier += *u.FrontierTokensNum
			tokenUnits++
		}
		if u.LocalRatio != nil {
			localSum += *u.LocalRatio
			ratioUnits++
		}
	}
	if tokenUnits > 0 {
		summary.FrontierTokens = fmt.Sprintf("%d (%d of %d units measured)", frontier, tokenUnits, len(units))
		summary.FrontierTokensNum = &frontier
	} else {
		summary.FrontierTokens = NotMeasured
	}
	if ratioUnits > 0 {
		mean := localSum / ratioUnits
		summary.LocalFirstRatio = fmt.Sprintf("%s (mean of %d of %d units)", formatPercent(mean), int(ratioUnits), len(units))
		summary.LocalRatio = &mean
	} else {
		summary.LocalFirstRatio = NotMeasured
	}
	var cacheSum float64
	cacheUnits := 0
	for _, u := range units {
		if u.CacheHitRatio != nil {
			cacheSum += *u.CacheHitRatio
			cacheUnits++
		}
	}
	if cacheUnits > 0 {
		mean := cacheSum / float64(cacheUnits)
		summary.PromptCacheHitRate = fmt.Sprintf("%s (mean of %d of %d units)", formatPercent(mean), cacheUnits, len(units))
		summary.CacheHitRatio = &mean
	} else {
		summary.PromptCacheHitRate = NotMeasured
	}
	summary.FactHitRatio = FollowUpRefs
	summary.ChecksBeforeReviews = FollowUpRefs
}

func summarizeSpend(units []UnitReport, spend *SpendReport, summary *MilestoneSummary) {
	attributed := 0.0
	for _, u := range units {
		if u.SpendAmount != nil {
			attributed += *u.SpendAmount
		}
	}
	allAttributed := 0.0
	for _, amount := range spend.SpendByPRNumber {
		allAttributed += amount
	}
	other := allAttributed - attributed
	if other < 0 {
		other = 0
	}
	summary.AttributedSpend, summary.AttributedSpendNum = formatSpend(attributed), &attributed
	summary.OtherUnitsSpend, summary.OtherUnitsSpendNum = formatSpend(other), &other
	summary.UnattributedSpend, summary.UnattributedSpendNum = formatSpend(spend.Unattributed), &spend.Unattributed
	total := spend.TotalSpend
	summary.TotalSpend, summary.TotalSpendNum = formatSpend(total), &total
}
