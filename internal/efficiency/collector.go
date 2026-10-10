// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// DefaultLimit is the number of landed pull requests reported when no limit is given.
const DefaultLimit = 20

// CollectorOptions sets configuration, input sources and overrides for the collector. Policy
// is the efficiency section of the manifest the caller already loaded (nil selects defaults);
// the collector never loads a manifest itself.
type CollectorOptions struct {
	Root           string
	Milestone      string
	Limit          int
	Policy         *config.EfficiencyPolicy
	ForgeDriver    forge.Forge
	ForgeJSONPath  string
	TranscriptsDir string
	SpendLogPath   string
	// Notes are caller-side findings, for example why no forge driver exists, printed with the report.
	Notes []string
}

// Collector coordinates gathering metrics from forge, transcripts and spend log.
type Collector struct {
	opts       CollectorOptions
	classifier *Classifier
}

// NewCollector constructs an efficiency metrics collector.
func NewCollector(opts CollectorOptions) *Collector {
	if opts.Root == "" {
		opts.Root = "."
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultLimit
	}
	return &Collector{opts: opts, classifier: NewClassifier(opts.Policy)}
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func formatPercent(val float64) string {
	return fmt.Sprintf("%.1f%%", val*100)
}

// formatSpend prints cents, or four decimals for sub-cent amounts so they do not read as zero.
func formatSpend(amount float64) string {
	if amount > 0 && amount < 0.01 {
		return fmt.Sprintf("$%.4f", amount)
	}
	return fmt.Sprintf("$%.2f", amount)
}

func formatTokens(tokens int64) string {
	return fmt.Sprintf("%d", tokens)
}

func newReport(milestone string) *Report {
	undefinedVector := VectorSummary{Display: UndefinedRate}
	return &Report{
		Units: make([]UnitReport, 0),
		MilestoneSummary: MilestoneSummary{
			Milestone:             milestone,
			MetricEpoch:           CurrentMetricEpoch,
			FrontierTokens:        UndefinedRate,
			AttributedSpend:       NotMeasured,
			SpendPerQualifiedUnit: UndefinedRate,
			UnattributedSpend:     NotMeasured,
			OtherUnitsSpend:       NotMeasured,
			TotalSpend:            NotMeasured,
			AvgIssueToMerge:       UndefinedRate,
			OperatorTouches:       UndefinedRate,
			PromptCacheHitRate:    UndefinedRate,
			LocalFirstRatio:       UndefinedRate,
			FactHitRatio:          UndefinedRate,
			ChecksBeforeReviews:   UndefinedRate,
			TokensByProvider:      undefinedVector,
			WallSeconds:           undefinedVector,
			ReviewRounds:          undefinedVector,
			Retries:               undefinedVector,
			OperatorMinutes:       undefinedVector,
			EscapedDefects:        undefinedVector,
		},
	}
}

// loaded is the set of pull requests of one run: all lists every pull request the forge source
// returned (for spend attribution), units the selected ones.
type loaded struct {
	all      []forge.MergedPullRequest
	units    []forge.MergedPullRequest
	measured bool
	live     bool // rows come from a live listing and carry no ledger fields of their own
}

// Collect executes the data collection and builds the final Report. A source that is
// configured but cannot be read fails the run.
func (c *Collector) Collect(ctx context.Context) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report := newReport(c.opts.Milestone)
	report.Notes = append(report.Notes, c.opts.Notes...)
	prs, err := c.loadForgePRs(ctx, report)
	if err != nil {
		return nil, err
	}
	report.Sources.Forge = prs.measured

	transStats, err := c.loadTranscripts(ctx, report)
	if err != nil {
		return nil, err
	}
	spend, err := c.loadSpend(ctx, report, prs.all)
	if err != nil {
		return nil, err
	}
	src := unitSources{owners: branchOwners(prs.all), transcript: transStats, spend: spend, measured: report.Sources, live: prs.live}
	for _, pr := range prs.units {
		unit, err := c.buildUnitReport(pr, src, report)
		if err != nil {
			return nil, err
		}
		report.Units = append(report.Units, unit)
	}
	applyRevertDispositions(report.Units)
	if err := ValidateRows(report.Units); err != nil {
		return nil, fmt.Errorf("validate efficiency units: %w", err)
	}
	if err := c.buildMilestoneSummary(report, spend); err != nil {
		return nil, fmt.Errorf("summarize efficiency units: %w", err)
	}
	return report, nil
}

func (c *Collector) loadTranscripts(ctx context.Context, report *Report) (map[string]*BranchTranscriptStats, error) {
	dir := c.resolve(c.opts.TranscriptsDir, c.transcriptsConfigured())
	if dir == "" {
		return nil, nil
	}
	stats, notes, err := ReadTranscriptsDir(ctx, dir, c.classifier)
	if err != nil {
		return nil, fmt.Errorf("read transcripts from %s: %w", dir, err)
	}
	report.Sources.Transcripts = true
	report.Notes = append(report.Notes, notes.Lines()...)
	return stats, nil
}

func (c *Collector) loadSpend(ctx context.Context, report *Report, prs []forge.MergedPullRequest) (*SpendReport, error) {
	path := c.resolve(c.opts.SpendLogPath, c.spendConfigured())
	if path == "" {
		return nil, nil
	}
	spend, err := ReadSpendLogFile(ctx, path, prs, c.classifier)
	if err != nil {
		return nil, fmt.Errorf("read spend log from %s: %w", path, err)
	}
	report.Sources.SpendLog = true
	if spend.DuplicateRequests > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("spend log %s: %d repeated request_id entries counted once", path, spend.DuplicateRequests))
	}
	return spend, nil
}

// resolve returns the flag value, else the manifest path joined to the root.
func (c *Collector) resolve(flag, configured string) string {
	if flag != "" {
		return flag
	}
	if configured == "" {
		return ""
	}
	return filepath.Join(c.opts.Root, configured)
}

func (c *Collector) transcriptsConfigured() string {
	if c.opts.Policy == nil {
		return ""
	}
	return c.opts.Policy.Sources.Transcripts.Directory()
}

func (c *Collector) transcriptsViaGateway() bool {
	if c.opts.Policy == nil {
		return false
	}
	return c.opts.Policy.Sources.TranscriptsViaGateway
}

func (c *Collector) spendConfigured() string {
	if c.opts.Policy == nil {
		return ""
	}
	return c.opts.Policy.Sources.SpendLog.Path
}

func (c *Collector) forgeConfigured() string {
	if c.opts.Policy == nil {
		return ""
	}
	return c.opts.Policy.Sources.Forge.Path
}

func (c *Collector) loadForgePRs(ctx context.Context, report *Report) (loaded, error) {
	if path := c.resolve(c.opts.ForgeJSONPath, c.forgeConfigured()); path != "" {
		all, err := forge.ReadMergedPullRequestsFile(path)
		if err != nil {
			return loaded{}, err
		}
		units, truncated := c.selectUnits(all)
		if truncated != "" {
			report.Notes = append(report.Notes, "forge records "+path+": "+truncated)
		}
		return loaded{all: all, units: units, measured: true}, nil
	}
	if c.opts.ForgeDriver == nil {
		return loaded{}, nil
	}
	report.Notes = append(report.Notes, "live forge queries merged pull requests only; unmerged rejected/abandoned units require forge records input", LiveDispositionNote, LiveUnmeasuredFields)
	list, err := c.opts.ForgeDriver.ListMergedPullRequests(ctx, forge.MergedPullRequestQuery{Limit: c.opts.Limit, Milestone: c.opts.Milestone})
	if err != nil {
		return loaded{}, fmt.Errorf("list merged pull requests: %w", err)
	}
	if list.Truncated != "" {
		report.Notes = append(report.Notes, "forge listing incomplete: "+list.Truncated)
	}
	for _, warning := range list.Warnings {
		report.Notes = append(report.Notes, "forge: "+warning)
	}
	return loaded{all: list.PullRequests, units: list.PullRequests, measured: true, live: true}, nil
}

// selectUnits filters by milestone first, sorts by merge time (newest first) and applies the
// limit, and says so when the limit cut matching pull requests.
func (c *Collector) selectUnits(all []forge.MergedPullRequest) ([]forge.MergedPullRequest, string) {
	matched := make([]forge.MergedPullRequest, 0, len(all))
	for _, pr := range all {
		if c.opts.Milestone == "" || pr.Milestone == c.opts.Milestone {
			matched = append(matched, pr)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].MergedAt.After(matched[j].MergedAt) })
	if len(matched) <= c.opts.Limit {
		return matched, ""
	}
	return matched[:c.opts.Limit], fmt.Sprintf("%d matching pull requests beyond the limit of %d were left out", len(matched)-c.opts.Limit, c.opts.Limit)
}
