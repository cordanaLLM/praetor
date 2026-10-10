package router

import (
	"fmt"
	"sort"
	"strings"
)

// EstimateError calculates actual cost minus estimated cost.
func EstimateError(estimated, actual float64) float64 {
	return actual - estimated
}

// FormatEstimateError renders the estimate error as signed currency and optional percentage.
func FormatEstimateError(estimated, actual float64) string {
	diff := EstimateError(estimated, actual)
	if estimated > 0 {
		return fmt.Sprintf("%+.4f (%+.1f%%)", diff, (diff/estimated)*100)
	}
	return fmt.Sprintf("%+.4f", diff)
}

// MeasuredCosts returns the pre-dispatch estimate and the actual cost of an identified outcome
// that carries both. ok is false otherwise: a missing estimate or actual cost is never zero, and
// an unidentified (legacy) record is never measured. It is the one rule every cost aggregation
// of outcomes applies.
func (o Outcome) MeasuredCosts() (estimate, actual float64, ok bool) {
	if o.Identity.PhysicalModel == "" || o.Identity.CostEstimate == nil || o.ActualCost == nil {
		return 0, 0, false
	}
	return *o.Identity.CostEstimate, *o.ActualCost, true
}

// CostTally sums the measured costs of a group of outcomes.
type CostTally struct {
	Runs          int
	MeasuredRuns  int
	EstimatedCost float64
	ActualCost    float64
}

// Add counts o as a run, and adds its costs when MeasuredCosts reports it measured.
func (t *CostTally) Add(o Outcome) {
	t.Runs++
	estimate, actual, ok := o.MeasuredCosts()
	if !ok {
		return
	}
	t.MeasuredRuns++
	t.EstimatedCost += estimate
	t.ActualCost += actual
}

// EstimateError returns actual minus estimated cost over the measured runs; ok is false when no
// run was measured.
func (t CostTally) EstimateError() (float64, bool) {
	if t.MeasuredRuns == 0 {
		return 0, false
	}
	return EstimateError(t.EstimatedCost, t.ActualCost), true
}

// TallyCosts tallies at most MaxOutcomeLines outcomes.
func TallyCosts(outcomes []Outcome) CostTally {
	var tally CostTally
	for i := 0; i < len(outcomes) && i < MaxOutcomeLines; i++ {
		tally.Add(outcomes[i])
	}
	return tally
}

// LaneReconciliation summarizes cost estimates, actual spend, and estimation errors for one lane.
// The cost fields cover the measured runs only.
type LaneReconciliation struct {
	Lane          string   `json:"lane"`
	Runs          int      `json:"runs"`
	MeasuredRuns  int      `json:"measured_runs"`
	EstimatedCost float64  `json:"estimated_cost"`
	ActualCost    float64  `json:"actual_cost"`
	EstimateError float64  `json:"estimate_error"`
	ErrorRatio    *float64 `json:"error_ratio,omitempty"`
}

// ReconcileLanes aggregates estimated and actual costs across outcomes grouped by lane.
func ReconcileLanes(outcomes []Outcome) []LaneReconciliation {
	groups := make(map[string]*CostTally)
	for i := 0; i < len(outcomes) && i < MaxOutcomeLines; i++ {
		lane := outcomes[i].Lane
		if lane == "" {
			lane = "default"
		}
		tally, ok := groups[lane]
		if !ok {
			tally = &CostTally{}
			groups[lane] = tally
		}
		tally.Add(outcomes[i])
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]LaneReconciliation, 0, len(names))
	for i := 0; i < len(names); i++ {
		result = append(result, laneReconciliation(names[i], *groups[names[i]]))
	}
	return result
}

func laneReconciliation(lane string, tally CostTally) LaneReconciliation {
	entry := LaneReconciliation{Lane: lane, Runs: tally.Runs, MeasuredRuns: tally.MeasuredRuns,
		EstimatedCost: tally.EstimatedCost, ActualCost: tally.ActualCost}
	diff, ok := tally.EstimateError()
	if !ok {
		return entry
	}
	entry.EstimateError = diff
	if tally.EstimatedCost > 0 {
		ratio := diff / tally.EstimatedCost
		entry.ErrorRatio = &ratio
	}
	return entry
}

// RenderLaneReconciliation formats the per-lane estimate-error metric table.
func RenderLaneReconciliation(reconciliations []LaneReconciliation) string {
	if len(reconciliations) == 0 {
		return "no outcomes recorded for reconciliation\n"
	}
	var sb strings.Builder
	for i := 0; i < len(reconciliations); i++ {
		r := reconciliations[i]
		if r.MeasuredRuns == 0 {
			fmt.Fprintf(&sb, "lane %s: estimate-error not measured (runs: %d, none with both an estimate and an actual cost)\n", r.Lane, r.Runs)
			continue
		}
		fmt.Fprintf(&sb, "lane %s: estimate-error %s (estimated: $%.4f, actual: $%.4f, measured runs: %d of %d)\n",
			r.Lane, FormatEstimateError(r.EstimatedCost, r.ActualCost), r.EstimatedCost, r.ActualCost, r.MeasuredRuns, r.Runs)
	}
	return sb.String()
}
