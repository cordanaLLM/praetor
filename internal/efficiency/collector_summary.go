// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// buildMilestoneSummary aggregates the units under the three rules MilestoneSummary names:
// resource consumption is the total over every unit divided by the qualified-unit count, latency
// is the mean over qualified units, ratios are pooled over the usage of every unit. Every
// display string names its rule and how many units it covers.
func (c *Collector) buildMilestoneSummary(report *Report, spend *SpendReport) error {
	summary := &report.MilestoneSummary
	summary.UnitsCount = len(report.Units)
	summary.MetricEpoch = CurrentMetricEpoch
	countLanes(report.Units, summary)
	summarizeIssueToMerge(report.Units, summary)
	summarizeUsageTotals(report.Units, summary)
	summarizeUsageRatios(report.Units, summary)
	if err := summarizeVectors(report.Units, summary); err != nil {
		return err
	}
	switch {
	case report.Sources.SpendLog && spend != nil:
		summarizeSpend(report.Units, spend, summary)
	case summary.QualifiedUnits > 0:
		summary.SpendPerQualifiedUnit = NotMeasured
	}
	summarizeEstimateError(report.Units, summary)
	return nil
}

func countLanes(units []UnitReport, summary *MilestoneSummary) {
	summary.LaneCounts = LaneCounts{}
	summary.PerLane = make(map[string]LaneCounts)
	for _, u := range units {
		summary.LaneCounts.Add(u.Disposition)
		if u.Lane != "" {
			lc := summary.PerLane[u.Lane]
			lc.Add(u.Disposition)
			summary.PerLane[u.Lane] = lc
		}
	}
	summary.QualifiedUnits = summary.LaneCounts.Qualified
}

// total is one resource sum. low and high follow value except where an interval field
// contributed its bounds, which bounded records.
type total struct {
	value, low, high float64
	bounded          bool
}

func (t *total) add(v float64) {
	t.value += v
	t.low += v
	t.high += v
}

func (t *total) addRange(v, low, high float64) {
	t.value += v
	t.low += low
	t.high += high
	t.bounded = true
}

// ruleScope is the coverage of one resource figure: carried of units carry it, and the total
// is divided by qualified.
type ruleScope struct {
	carried, units, qualified int
}

// figureFormat prints a resource total and its per-qualified-unit rate.
type figureFormat struct {
	total, rate func(float64) string
}

var (
	countFormat = figureFormat{
		total: func(v float64) string { return fmt.Sprintf("%.0f", v) },
		rate:  func(v float64) string { return fmt.Sprintf("%.2f", v) },
	}
	secondsFormat = figureFormat{total: formatSeconds, rate: formatSeconds}
	minutesFormat = figureFormat{total: formatMinutes, rate: formatMinutes}
	spendFormat   = figureFormat{total: formatSpend, rate: formatSpend}
)

// formatSeconds keeps the seconds below an hour, so interval bounds a minute apart stay apart.
func formatSeconds(v float64) string {
	d := time.Duration(v * float64(time.Second)).Round(time.Second)
	if d < time.Minute || d >= time.Hour {
		return formatDuration(d)
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func formatMinutes(v float64) string { return fmt.Sprintf("%.1fm", v) }

// bracketed prints t divided by divisor, with its bounds when an interval contributed.
func bracketed(print func(float64) string, t total, divisor float64) string {
	text := print(t.value / divisor)
	if t.bounded {
		text += fmt.Sprintf(" [%s, %s]", print(t.low/divisor), print(t.high/divisor))
	}
	return text
}

// perQualifiedText prints a resource figure as cost per qualified unit with the rule named:
// undefined when no unit qualified, not measured when no unit carries the figure, and a lower
// bound when only some units do (resource figures are never negative).
func perQualifiedText(s ruleScope, render func() (rate, sum string)) string {
	switch {
	case s.qualified == 0:
		return UndefinedRate
	case s.carried == 0:
		return NotMeasured
	}
	rate, sum := render()
	if s.carried < s.units {
		return fmt.Sprintf(">= %s per qualified unit (lower bound: total %s over %d of %d units measured / %d qualified)", rate, sum, s.carried, s.units, s.qualified)
	}
	return fmt.Sprintf("%s per qualified unit (total %s over %d units / %d qualified)", rate, sum, s.units, s.qualified)
}

func scalarText(t total, s ruleScope, f figureFormat) string {
	return perQualifiedText(s, func() (string, string) {
		return bracketed(f.rate, t, float64(s.qualified)), bracketed(f.total, t, 1)
	})
}

// perQualifiedValue is the rate behind perQualifiedText, nil where the text is not a number.
func perQualifiedValue(t total, s ruleScope) *float64 {
	if s.qualified == 0 || s.carried == 0 {
		return nil
	}
	rate := t.value / float64(s.qualified)
	return &rate
}

// summarizeIssueToMerge is the latency rule: the mean over qualified units that measured it.
func summarizeIssueToMerge(units []UnitReport, summary *MilestoneSummary) {
	if summary.QualifiedUnits == 0 {
		summary.AvgIssueToMerge = UndefinedRate
		return
	}
	var sum int64
	measured := 0
	for _, u := range units {
		if u.Disposition != DispositionQualified || u.IssueToMergeSecs == nil {
			continue
		}
		sum += *u.IssueToMergeSecs
		measured++
	}
	summary.IssueToMergeUnits = measured
	if measured == 0 {
		summary.AvgIssueToMerge = NotMeasured
		return
	}
	avg := sum / int64(measured)
	summary.AvgIssueToMergeSecs = &avg
	summary.AvgIssueToMerge = fmt.Sprintf("%s (mean over %d of %d qualified units measured)", formatDuration(time.Duration(avg)*time.Second), measured, summary.QualifiedUnits)
}

// summarizeUsageTotals applies the resource rule to operator touches and frontier tokens.
func summarizeUsageTotals(units []UnitReport, summary *MilestoneSummary) {
	var touches, tokens total
	touchScope := ruleScope{units: len(units), qualified: summary.QualifiedUnits}
	tokenScope := touchScope
	for _, u := range units {
		if u.OperatorTouchNum != nil {
			touches.add(float64(*u.OperatorTouchNum))
			touchScope.carried++
		}
		if u.FrontierTokensNum != nil {
			tokens.add(float64(*u.FrontierTokensNum))
			tokenScope.carried++
		}
	}
	summary.OperatorTouches = scalarText(touches, touchScope, countFormat)
	summary.OperatorTouchesPerQualified = perQualifiedValue(touches, touchScope)
	if touchScope.carried > 0 {
		n := int(touches.value)
		summary.OperatorTouchNum = &n
	}
	summary.FrontierTokens = scalarText(tokens, tokenScope, countFormat)
	summary.FrontierTokensPerQualifiedUnit = perQualifiedValue(tokens, tokenScope)
	if tokenScope.carried > 0 {
		n := int64(tokens.value)
		summary.FrontierTokensNum = &n
	}
}

// pooledRatio is the ratio rule: numerator over denominator summed across every unit that
// carries the counts, whatever its disposition.
func pooledRatio(num, denom float64, carried, units int) (string, *float64) {
	switch {
	case units == 0:
		return UndefinedRate, nil
	case carried == 0:
		return NotMeasured, nil
	case denom == 0:
		return UndefinedRate, nil
	}
	ratio := num / denom
	return fmt.Sprintf("%s (pooled over the usage of %d of %d units)", formatPercent(ratio), carried, units), &ratio
}

// summarizeUsageRatios pools the local-first ratio and the prompt-cache hit rate over all usage.
func summarizeUsageRatios(units []UnitReport, summary *MilestoneSummary) {
	var requests, local, cacheRead, cacheInput float64
	requestUnits, cacheUnits := 0, 0
	for _, u := range units {
		if u.RequestsNum != nil && u.LocalRequestsNum != nil {
			requests += float64(*u.RequestsNum)
			local += float64(*u.LocalRequestsNum)
			requestUnits++
		}
		if u.CacheReadTokensNum != nil && u.PromptInputTokensNum != nil {
			cacheRead += float64(*u.CacheReadTokensNum)
			cacheInput += float64(*u.PromptInputTokensNum)
			cacheUnits++
		}
	}
	summary.LocalFirstRatio, summary.LocalRatio = pooledRatio(local, requests, requestUnits, len(units))
	summary.PromptCacheHitRate, summary.CacheHitRatio = pooledRatio(cacheRead, cacheInput, cacheUnits, len(units))
	summary.FactHitRatio, summary.ChecksBeforeReviews = UndefinedRate, UndefinedRate
	if len(units) > 0 {
		summary.FactHitRatio, summary.ChecksBeforeReviews = FollowUpRefs, FollowUpRefs
	}
}

// summarizeSpend splits the spend log and applies the resource rule to the listed units' spend.
func summarizeSpend(units []UnitReport, spend *SpendReport, summary *MilestoneSummary) {
	var attributed total
	scope := ruleScope{units: len(units), qualified: summary.QualifiedUnits}
	for _, u := range units {
		if u.SpendAmount != nil {
			attributed.add(*u.SpendAmount)
			scope.carried++
		}
	}
	listed := attributed.value
	summary.AttributedSpend, summary.AttributedSpendNum = formatSpend(listed), &listed
	summary.SpendPerQualifiedUnit = scalarText(attributed, scope, spendFormat)
	summary.SpendPerQualifiedUnitNum = perQualifiedValue(attributed, scope)
	allAttributed := 0.0
	for _, amount := range spend.SpendByPRNumber {
		allAttributed += amount
	}
	other := allAttributed - listed
	if other < 0 {
		other = 0
	}
	summary.OtherUnitsSpend, summary.OtherUnitsSpendNum = formatSpend(other), &other
	summary.UnattributedSpend, summary.UnattributedSpendNum = formatSpend(spend.Unattributed), &spend.Unattributed
	totalSpend := spend.TotalSpend
	summary.TotalSpend, summary.TotalSpendNum = formatSpend(totalSpend), &totalSpend
}

// vectorTotals accumulates one vector field over the units that carry it.
type vectorTotals struct {
	carried int
	mix     ProvenanceMix
	byKey   map[string]*total
}

func (v *vectorTotals) add(r vectorReading) {
	v.carried++
	v.mix.add(r.provenance)
	if v.byKey == nil {
		v.byKey = make(map[string]*total)
	}
	for key, value := range r.value {
		t := v.byKey[key]
		if t == nil {
			t = &total{}
			v.byKey[key] = t
		}
		if r.provenance == ProvenanceInterval {
			t.addRange(value, r.low[key], r.high[key])
		} else {
			t.add(value)
		}
	}
}

func (v vectorTotals) keys() []string {
	return slices.Sorted(maps.Keys(v.byKey))
}

// texts prints the per-qualified rate and the total of every component.
func (v vectorTotals) texts(qualified int, f figureFormat) (string, string) {
	keys := v.keys()
	if len(keys) == 0 {
		return f.rate(0), f.total(0)
	}
	if len(keys) == 1 && keys[0] == "" {
		t := *v.byKey[""]
		return bracketed(f.rate, t, float64(qualified)), bracketed(f.total, t, 1)
	}
	rates := make([]string, 0, len(keys))
	sums := make([]string, 0, len(keys))
	for _, key := range keys {
		t := *v.byKey[key]
		rates = append(rates, key+": "+bracketed(f.rate, t, float64(qualified)))
		sums = append(sums, key+": "+bracketed(f.total, t, 1))
	}
	return strings.Join(rates, ", "), strings.Join(sums, ", ")
}

func (v vectorTotals) components(qualified int) []VectorComponent {
	out := make([]VectorComponent, 0, len(v.byKey))
	for _, key := range v.keys() {
		t := *v.byKey[key]
		c := VectorComponent{Key: key, Total: t.value}
		if t.bounded {
			c.TotalBounds = &Interval{Low: t.low, High: t.high}
		}
		if qualified > 0 {
			q := float64(qualified)
			rate := t.value / q
			c.PerQualifiedUnit = &rate
			if t.bounded {
				c.PerQualifiedUnitBounds = &Interval{Low: t.low / q, High: t.high / q}
			}
		}
		out = append(out, c)
	}
	return out
}

// zeroClaim reports a total of zero that every unit measured: the case FormatZeroFailureClaim
// words with its rule-of-three bound. A modeled, cited or interval zero supports no such claim.
func (v vectorTotals) zeroClaim(units int) bool {
	if v.carried < units || v.mix.Measured != v.carried {
		return false
	}
	for _, t := range v.byKey {
		if t.value != 0 {
			return false
		}
	}
	return true
}

// summary applies the resource rule to the field and appends the provenance mix to the text.
func (v vectorTotals) summary(units, qualified int, f figureFormat, failureCounter bool) VectorSummary {
	s := VectorSummary{UnitsMeasured: v.carried, LowerBound: v.carried > 0 && v.carried < units, Provenance: v.mix}
	if v.carried > 0 {
		s.Components = v.components(qualified)
	}
	scope := ruleScope{carried: v.carried, units: units, qualified: qualified}
	s.Display = perQualifiedText(scope, func() (string, string) { return v.texts(qualified, f) })
	if failureCounter && qualified > 0 && v.zeroClaim(units) {
		s.Display = FormatZeroFailureClaim(qualified, units)
	}
	if qualified > 0 && v.carried > 0 {
		s.Display += " [" + v.mix.String() + "]"
	}
	return s
}

// summarizeVectors applies the resource rule to the six vector fields.
func summarizeVectors(units []UnitReport, summary *MilestoneSummary) error {
	var totals [len(vectorFieldNames)]vectorTotals
	for _, u := range units {
		fields, err := u.vectors()
		if err != nil {
			return fmt.Errorf("unit #%d: %w", u.PullRequestNumber, err)
		}
		for i, f := range fields {
			if f.reading != nil {
				totals[i].add(*f.reading)
			}
		}
	}
	n, q := len(units), summary.QualifiedUnits
	summary.TokensByProvider = totals[0].summary(n, q, countFormat, false)
	summary.WallSeconds = totals[1].summary(n, q, secondsFormat, false)
	summary.ReviewRounds = totals[2].summary(n, q, countFormat, false)
	summary.Retries = totals[3].summary(n, q, countFormat, false)
	summary.OperatorMinutes = totals[4].summary(n, q, minutesFormat, false)
	summary.EscapedDefects = totals[5].summary(n, q, countFormat, true)
	return nil
}

func summarizeEstimateError(units []UnitReport, summary *MilestoneSummary) {
	var total float64
	measured := 0
	for _, u := range units {
		if u.EstimateErrorAmount != nil {
			total += *u.EstimateErrorAmount
			measured++
		}
	}
	if measured > 0 {
		summary.EstimateError = formatEstimateErrorSpend(total)
		summary.EstimateErrorAmount = &total
	}
}
