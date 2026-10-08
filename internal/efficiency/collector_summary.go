// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"time"
)

// buildMilestoneSummary aggregates the units. Every average and ratio runs over the units that
// have the metric and says how many; a zero denominator prints not measured.
func (c *Collector) buildMilestoneSummary(report *Report, spend *SpendReport) {
	summary := &report.MilestoneSummary
	summary.UnitsCount = len(report.Units)
	if len(report.Units) == 0 {
		return
	}
	summarizeIssueToMerge(report.Units, summary)
	summarizeTranscriptMetrics(report.Units, summary)
	summarizeGatewayAndTranscriptUsage(report.Units, summary)
	if report.Sources.SpendLog && spend != nil {
		summarizeSpend(report.Units, spend, summary)
	}
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
	}
	if ratioUnits > 0 {
		mean := localSum / ratioUnits
		summary.LocalFirstRatio = fmt.Sprintf("%s (mean of %d of %d units)", formatPercent(mean), int(ratioUnits), len(units))
		summary.LocalRatio = &mean
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
	}
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
