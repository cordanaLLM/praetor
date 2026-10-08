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

// CollectorOptions sets configuration, input sources and overrides for the collector.
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

// Collector coordinates gathering metrics from forge, transcripts and spend log.
type Collector struct {
	opts       CollectorOptions
	classifier *Classifier
	initErr    error
}

// NewCollector constructs an efficiency metrics collector.
func NewCollector(opts CollectorOptions) *Collector {
	if opts.Root == "" {
		opts.Root = "."
	}
	policy := opts.Policy
	var initErr error
	if policy == nil {
		p, err := config.RepositoryEfficiencyPolicy(opts.Root)
		if err != nil {
			initErr = fmt.Errorf("load efficiency policy: %w", err)
		} else {
			policy = p
		}
	}
	return &Collector{
		opts:       opts,
		classifier: NewClassifier(policy),
		initErr:    initErr,
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

func newReport(milestone string) *Report {
	return &Report{
		Units: make([]UnitReport, 0),
		MilestoneSummary: MilestoneSummary{
			Milestone:           milestone,
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
}

// Collect executes the data collection and builds the final Report.
func (c *Collector) Collect(ctx context.Context) (*Report, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	report := newReport(c.opts.Milestone)

	prs, forgeMeasured, err := c.loadForgePRs(ctx)
	if err != nil {
		return nil, err
	}
	report.Sources.Forge = forgeMeasured

	var transStats map[string]*BranchTranscriptStats
	transDir := c.resolveTranscriptsDir()
	if transDir != "" {
		stats, transErr := ReadTranscriptsDir(ctx, transDir, c.classifier)
		if transErr != nil {
			return nil, fmt.Errorf("read transcripts from %s: %w", transDir, transErr)
		}
		transStats = stats
		report.Sources.Transcripts = true
	}

	var spendReport *SpendReport
	spendPath := c.resolveSpendLogPath()
	if spendPath != "" {
		sr, spendErr := ReadSpendLogFile(ctx, spendPath, prs, c.classifier)
		if spendErr != nil {
			return nil, fmt.Errorf("read spend log from %s: %w", spendPath, spendErr)
		}
		spendReport = sr
		report.Sources.SpendLog = true
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
	var filtered []forge.MergedPullRequest
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

func (c *Collector) calculateIssueToMerge(pr forge.MergedPullRequest) (string, *int64) {
	if pr.MergedAt.IsZero() {
		return NotMeasured, nil
	}
	earliestCreated := earliestClosingIssueCreatedAt(pr.ClosingIssues)
	// If closing issues are linked but none have created_at measured, do not silently fall back to PR age.
	if len(pr.ClosingIssues) > 0 && earliestCreated.IsZero() {
		return NotMeasured, nil
	}
	isPRFallback := false
	if earliestCreated.IsZero() {
		if pr.CreatedAt.IsZero() {
			return NotMeasured, nil
		}
		earliestCreated = pr.CreatedAt
		isPRFallback = true
	}
	dur := pr.MergedAt.Sub(earliestCreated)
	if dur < 0 {
		dur = 0
	}
	secs := int64(dur.Seconds())
	res := formatDuration(dur)
	if isPRFallback {
		res += " (PR)"
	}
	return res, &secs
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

	stats := transStats[pr.HeadBranch]
	if sources.Transcripts && stats != nil {
		touches := stats.OperatorTouches
		unit.OperatorTouches = fmt.Sprintf("%d", touches)
		unit.OperatorTouchNum = &touches

		if rate, ok := stats.PromptCacheHitRate(); ok {
			unit.PromptCacheHitRate = formatPercent(rate)
			unit.CacheHitRatio = &rate
		}
	}

	if sources.SpendLog {
		spend := 0.0
		if spendReport != nil {
			spend = spendReport.SpendByPRNumber[pr.Number]
		}
		unit.Spend = formatSpend(spend)
		unit.SpendAmount = &spend
	}

	applyCombinedTokensAndLocality(&unit, stats, spendReport, pr.Number, sources)
	return unit
}

func applyCombinedTokensAndLocality(unit *UnitReport, stats *BranchTranscriptStats, spendReport *SpendReport, prNum int, sources SourcesMeasured) {
	var totalFrontier int64
	hasTokens := false
	if stats != nil {
		totalFrontier += stats.FrontierTokens
		hasTokens = true
	}
	if spendReport != nil {
		if ft, ok := spendReport.FrontierTokensByPRNumber[prNum]; ok {
			totalFrontier += ft
			hasTokens = true
		}
	}
	if hasTokens && (sources.Transcripts || sources.SpendLog) {
		unit.FrontierTokens = formatTokens(totalFrontier)
		unit.FrontierTokensNum = &totalFrontier
	}

	var totalReqs, localReqs int
	if stats != nil {
		totalReqs += stats.TotalRequests
		localReqs += stats.LocalRequests
	}
	if spendReport != nil {
		totalReqs += spendReport.RequestsByPRNumber[prNum]
		localReqs += spendReport.LocalRequestsByPRNumber[prNum]
	}
	if totalReqs > 0 {
		ratio := float64(localReqs) / float64(totalReqs)
		unit.LocalFirstRatio = formatPercent(ratio)
		unit.LocalRatio = &ratio
	}
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
	if report.Sources.Transcripts || report.Sources.SpendLog {
		summarizeTranscriptsAndSpend(prs, transStats, spendReport, report.Sources, summary)
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

type transcriptTotals struct {
	frontierTokens int64
	touches        int
	cacheRead      int64
	cacheCreation  int64
	inputTokens    int64
	localReqs      int
	totalReqs      int
	hasTokens      bool
}

func aggregateTranscripts(prs []forge.MergedPullRequest, transStats map[string]*BranchTranscriptStats) transcriptTotals {
	var t transcriptTotals
	for _, pr := range prs {
		stats := transStats[pr.HeadBranch]
		if stats == nil {
			continue
		}
		t.frontierTokens += stats.FrontierTokens
		t.touches += stats.OperatorTouches
		t.cacheRead += stats.CacheReadTokens
		t.cacheCreation += stats.CacheCreationTokens
		t.inputTokens += stats.InputTokens
		t.localReqs += stats.LocalRequests
		t.totalReqs += stats.TotalRequests
		t.hasTokens = true
	}
	return t
}

func summarizeTranscriptsAndSpend(prs []forge.MergedPullRequest, transStats map[string]*BranchTranscriptStats, spendReport *SpendReport, sources SourcesMeasured, summary *MilestoneSummary) {
	var totalFrontier int64
	var totalLocalReqs, totalReqs int
	hasTokens := false

	if sources.Transcripts {
		t := aggregateTranscripts(prs, transStats)
		totalFrontier += t.frontierTokens
		totalLocalReqs += t.localReqs
		totalReqs += t.totalReqs
		hasTokens = t.hasTokens
		summary.OperatorTouches = fmt.Sprintf("%d", t.touches)
		summary.OperatorTouchNum = &t.touches

		cacheDenom := t.inputTokens + t.cacheCreation + t.cacheRead
		if cacheDenom > 0 {
			rate := float64(t.cacheRead) / float64(cacheDenom)
			summary.PromptCacheHitRate = formatPercent(rate)
			summary.CacheHitRatio = &rate
		}
	}

	if sources.SpendLog && spendReport != nil {
		for _, pr := range prs {
			totalReqs += spendReport.RequestsByPRNumber[pr.Number]
			totalLocalReqs += spendReport.LocalRequestsByPRNumber[pr.Number]
			if ft, ok := spendReport.FrontierTokensByPRNumber[pr.Number]; ok {
				totalFrontier += ft
				hasTokens = true
			}
		}
	}

	if hasTokens {
		summary.FrontierTokens = formatTokens(totalFrontier)
		summary.FrontierTokensNum = &totalFrontier
	}

	if totalReqs > 0 {
		ratio := float64(totalLocalReqs) / float64(totalReqs)
		summary.LocalFirstRatio = formatPercent(ratio)
		summary.LocalRatio = &ratio
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
