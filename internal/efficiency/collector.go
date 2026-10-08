// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// CollectorOptions supplies inputs and overrides for efficiency metrics collection.
type CollectorOptions struct {
	Root           string
	Milestone      string
	Limit          int
	Policy         *config.EfficiencyPolicy
	ForgeDriver    forge.Forge
	ForgeJSONPath  string
	TranscriptsDir string
	SpendLogPath   string
}

// Collector coordinates loading sources and assembling the efficiency report.
type Collector struct {
	opts       CollectorOptions
	classifier *Classifier
}

// NewCollector constructs a Collector with validated options.
func NewCollector(opts CollectorOptions) *Collector {
	if opts.Root == "" {
		opts.Root = "."
	}
	policy := opts.Policy
	if policy == nil {
		p, err := config.RepositoryEfficiencyPolicy(opts.Root)
		if err == nil {
			policy = p
		}
	}
	return &Collector{
		opts:       opts,
		classifier: NewClassifier(policy),
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	return fmt.Sprintf("%dd%dh", days, hours)
}

func formatPercent(val float64) string {
	return fmt.Sprintf("%.1f%%", val*100)
}

func formatSpend(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

func formatTokens(tokens int64) string {
	return fmt.Sprintf("%d", tokens)
}

// Collect executes the data collection and builds the final Report.
func (c *Collector) Collect(ctx context.Context) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	report := &Report{
		Units: make([]UnitReport, 0),
		MilestoneSummary: MilestoneSummary{
			Milestone:           c.opts.Milestone,
			FrontierTokens:      NotMeasured,
			AttributedSpend:     NotMeasured,
			UnattributedSpend:   NotMeasured,
			TotalSpend:          NotMeasured,
			AvgIssueToMerge:     NotMeasured,
			OperatorTouches:     NotMeasured,
			PromptCacheHitRate:  NotMeasured,
			LocalFirstRatio:     NotMeasured,
			FactHitRatio:        FollowUpRefs,
			ChecksBeforeReviews: FollowUpRefs,
		},
	}

	prs, forgeMeasured, err := c.loadForgePRs(ctx)
	if err != nil {
		return nil, err
	}
	report.Sources.Forge = forgeMeasured

	var transStats map[string]*BranchTranscriptStats
	transDir := c.resolveTranscriptsDir()
	if transDir != "" {
		stats, transErr := ReadTranscriptsDir(ctx, transDir, c.classifier)
		if transErr == nil {
			transStats = stats
			report.Sources.Transcripts = true
		}
	}

	var spendReport *SpendReport
	spendPath := c.resolveSpendLogPath()
	if spendPath != "" {
		sr, spendErr := ReadSpendLogFile(ctx, spendPath, prs)
		if spendErr == nil {
			spendReport = sr
			report.Sources.SpendLog = true
		}
	}

	filteredPRs := c.filterPRs(prs)
	for _, pr := range filteredPRs {
		unit := c.buildUnitReport(pr, transStats, spendReport, report.Sources)
		report.Units = append(report.Units, unit)
	}

	c.buildMilestoneSummary(report, filteredPRs, transStats, spendReport)
	return report, nil
}

func (c *Collector) resolveForgePath() string {
	if c.opts.ForgeJSONPath != "" {
		return c.opts.ForgeJSONPath
	}
	if c.opts.Policy != nil && c.opts.Policy.Sources.Forge.Path != "" {
		return filepath.Join(c.opts.Root, c.opts.Policy.Sources.Forge.Path)
	}
	return ""
}

func (c *Collector) resolveTranscriptsDir() string {
	if c.opts.TranscriptsDir != "" {
		return c.opts.TranscriptsDir
	}
	if c.opts.Policy != nil && c.opts.Policy.Sources.Transcripts.Directory() != "" {
		return filepath.Join(c.opts.Root, c.opts.Policy.Sources.Transcripts.Directory())
	}
	return ""
}

func (c *Collector) resolveSpendLogPath() string {
	if c.opts.SpendLogPath != "" {
		return c.opts.SpendLogPath
	}
	if c.opts.Policy != nil && c.opts.Policy.Sources.SpendLog.Path != "" {
		return filepath.Join(c.opts.Root, c.opts.Policy.Sources.SpendLog.Path)
	}
	return ""
}

func (c *Collector) loadForgePRs(ctx context.Context) ([]forge.MergedPullRequest, bool, error) {
	if forgePath := c.resolveForgePath(); forgePath != "" {
		prs, err := forge.ReadMergedPullRequestsFile(forgePath)
		if err != nil {
			return nil, false, err
		}
		return prs, true, nil
	}
	if c.opts.ForgeDriver != nil {
		limit := c.opts.Limit
		if limit <= 0 {
			limit = 100
		}
		prs, err := c.opts.ForgeDriver.ListMergedPullRequests(ctx, limit)
		if err != nil {
			return nil, false, err
		}
		return prs, true, nil
	}
	return nil, false, nil
}

func (c *Collector) filterPRs(prs []forge.MergedPullRequest) []forge.MergedPullRequest {
	if len(prs) == 0 {
		return nil
	}
	filtered := make([]forge.MergedPullRequest, 0, len(prs))
	for _, pr := range prs {
		if c.opts.Milestone != "" && pr.Milestone != c.opts.Milestone {
			continue
		}
		filtered = append(filtered, pr)
		if c.opts.Limit > 0 && len(filtered) >= c.opts.Limit {
			break
		}
	}
	return filtered
}

func (c *Collector) calculateIssueToMerge(pr forge.MergedPullRequest) (string, *int64) {
	if pr.MergedAt.IsZero() {
		return NotMeasured, nil
	}
	var earliestCreated time.Time
	for _, ci := range pr.ClosingIssues {
		if !ci.CreatedAt.IsZero() && (earliestCreated.IsZero() || ci.CreatedAt.Before(earliestCreated)) {
			earliestCreated = ci.CreatedAt
		}
	}
	if earliestCreated.IsZero() && !pr.CreatedAt.IsZero() {
		earliestCreated = pr.CreatedAt
	}
	if earliestCreated.IsZero() {
		return NotMeasured, nil
	}
	dur := pr.MergedAt.Sub(earliestCreated)
	if dur < 0 {
		dur = 0
	}
	secs := int64(dur.Seconds())
	return formatDuration(dur), &secs
}

func (c *Collector) buildUnitReport(pr forge.MergedPullRequest, transStats map[string]*BranchTranscriptStats, spendReport *SpendReport, sources SourcesMeasured) UnitReport {
	closingNums := make([]int, 0, len(pr.ClosingIssues))
	for _, ci := range pr.ClosingIssues {
		closingNums = append(closingNums, ci.Number)
	}

	itmStr, itmSecs := c.calculateIssueToMerge(pr)

	unit := UnitReport{
		PullRequestNumber:   pr.Number,
		HeadBranch:          pr.HeadBranch,
		Title:               pr.Title,
		Milestone:           pr.Milestone,
		CreatedAt:           pr.CreatedAt,
		MergedAt:            pr.MergedAt,
		ClosingIssues:       closingNums,
		IssueToMerge:        itmStr,
		IssueToMergeSecs:    itmSecs,
		FrontierTokens:      NotMeasured,
		Spend:               NotMeasured,
		OperatorTouches:     NotMeasured,
		PromptCacheHitRate:  NotMeasured,
		LocalFirstRatio:     NotMeasured,
		FactHitRatio:        FollowUpRefs,
		ChecksBeforeReviews: FollowUpRefs,
	}

	if sources.Transcripts {
		applyTranscriptStatsToUnit(&unit, transStats[pr.HeadBranch])
	}
	if sources.SpendLog {
		applySpendReportToUnit(&unit, spendReport, pr.Number)
	}
	return unit
}

func applyTranscriptStatsToUnit(unit *UnitReport, stats *BranchTranscriptStats) {
	if stats == nil {
		applyZeroTranscriptStats(unit)
		return
	}
	touches := stats.OperatorTouches
	unit.OperatorTouches = fmt.Sprintf("%d", touches)
	unit.OperatorTouchNum = &touches

	unit.FrontierTokens = formatTokens(stats.FrontierTokens)
	fTokens := stats.FrontierTokens
	unit.FrontierTokensNum = &fTokens

	if rate, ok := stats.PromptCacheHitRate(); ok {
		unit.PromptCacheHitRate = formatPercent(rate)
		unit.CacheHitRatio = &rate
	} else {
		unit.PromptCacheHitRate = "0.0%"
		zero := 0.0
		unit.CacheHitRatio = &zero
	}

	if ratio, ok := stats.LocalFirstRatio(); ok {
		unit.LocalFirstRatio = formatPercent(ratio)
		unit.LocalRatio = &ratio
	} else {
		unit.LocalFirstRatio = "0.0%"
		zero := 0.0
		unit.LocalRatio = &zero
	}
}

func applyZeroTranscriptStats(unit *UnitReport) {
	zero := 0
	zero64 := int64(0)
	zeroF := 0.0
	unit.OperatorTouches = "0"
	unit.OperatorTouchNum = &zero
	unit.FrontierTokens = "0"
	unit.FrontierTokensNum = &zero64
	unit.PromptCacheHitRate = "0.0%"
	unit.CacheHitRatio = &zeroF
	unit.LocalFirstRatio = "0.0%"
	unit.LocalRatio = &zeroF
}

func applySpendReportToUnit(unit *UnitReport, spendReport *SpendReport, prNum int) {
	spend := 0.0
	if spendReport != nil {
		spend = spendReport.SpendByPRNumber[prNum]
	}
	unit.Spend = formatSpend(spend)
	unit.SpendAmount = &spend
}

func (c *Collector) buildMilestoneSummary(report *Report, prs []forge.MergedPullRequest, transStats map[string]*BranchTranscriptStats, spendReport *SpendReport) {
	summary := &report.MilestoneSummary
	summary.UnitsCount = len(report.Units)

	if len(report.Units) == 0 {
		return
	}

	if report.Sources.Forge {
		summarizeForge(report, summary)
	}
	if report.Sources.Transcripts {
		summarizeTranscripts(prs, transStats, summary)
	}
	if report.Sources.SpendLog {
		summarizeSpend(report, spendReport, summary)
	}
}

func summarizeForge(report *Report, summary *MilestoneSummary) {
	var totalDurSecs int64
	measuredCount := int64(0)
	for _, u := range report.Units {
		if u.IssueToMergeSecs != nil {
			totalDurSecs += *u.IssueToMergeSecs
			measuredCount++
		}
	}
	if measuredCount > 0 {
		avgSecs := totalDurSecs / measuredCount
		summary.AvgIssueToMerge = formatDuration(time.Duration(avgSecs) * time.Second)
		summary.AvgIssueToMergeSecs = &avgSecs
	}
}

func summarizeTranscripts(prs []forge.MergedPullRequest, transStats map[string]*BranchTranscriptStats, summary *MilestoneSummary) {
	var totalFrontier int64
	var totalTouches int
	var totalRead, totalCacheCreation, totalInput int64
	var totalLocalReqs, totalReqs int

	for _, pr := range prs {
		if stats := transStats[pr.HeadBranch]; stats != nil {
			totalFrontier += stats.FrontierTokens
			totalTouches += stats.OperatorTouches
			totalRead += stats.CacheReadTokens
			totalCacheCreation += stats.CacheCreationTokens
			totalInput += stats.InputTokens
			totalLocalReqs += stats.LocalRequests
			totalReqs += stats.TotalRequests
		}
	}

	summary.FrontierTokens = formatTokens(totalFrontier)
	summary.FrontierTokensNum = &totalFrontier
	summary.OperatorTouches = fmt.Sprintf("%d", totalTouches)
	summary.OperatorTouchNum = &totalTouches

	cacheDenom := totalInput + totalCacheCreation + totalRead
	if cacheDenom > 0 {
		rate := float64(totalRead) / float64(cacheDenom)
		summary.PromptCacheHitRate = formatPercent(rate)
		summary.CacheHitRatio = &rate
	} else {
		summary.PromptCacheHitRate = "0.0%"
		zero := 0.0
		summary.CacheHitRatio = &zero
	}

	if totalReqs > 0 {
		ratio := float64(totalLocalReqs) / float64(totalReqs)
		summary.LocalFirstRatio = formatPercent(ratio)
		summary.LocalRatio = &ratio
	} else {
		summary.LocalFirstRatio = "0.0%"
		zero := 0.0
		summary.LocalRatio = &zero
	}
}

func summarizeSpend(report *Report, spendReport *SpendReport, summary *MilestoneSummary) {
	attrSpend := 0.0
	for _, u := range report.Units {
		if u.SpendAmount != nil {
			attrSpend += *u.SpendAmount
		}
	}
	summary.AttributedSpend = formatSpend(attrSpend)
	summary.AttributedSpendNum = &attrSpend

	unattrSpend := 0.0
	if spendReport != nil {
		unattrSpend = spendReport.Unattributed
	}
	summary.UnattributedSpend = formatSpend(unattrSpend)
	summary.UnattributedSpendNum = &unattrSpend

	totSpend := attrSpend + unattrSpend
	summary.TotalSpend = formatSpend(totSpend)
	summary.TotalSpendNum = &totSpend
}
