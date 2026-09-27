package hiss

import (
	"fmt"
	"go/token"
	"strings"
)

// MaxListedMeasurements bounds the measurement lines one report prints. The report keeps
// every measurement up to the scan cap; the rest are counted on the last printed line.
const MaxListedMeasurements = 200

// complexityRule is the invariant every complexity measurement belongs to.
const complexityRule = "HISS-04"

// Severity says what a finding does to a verdict.
type Severity string

// SeverityReport marks a finding that is measured and printed but never enforced: it does
// not count toward TotalInfractions, the rule breakdown, the debt baseline or any gate.
const SeverityReport Severity = "report"

// MeasurementKind names the HISS-04 metric a measurement exceeds.
type MeasurementKind string

// The complexity metrics the scanner measures. Function length is not among them: it is
// enforced as a violation.
const (
	KindCyclomatic MeasurementKind = "cyclomatic"
	KindCognitive  MeasurementKind = "cognitive"
	KindStatements MeasurementKind = "statements"
)

// kindLabels renders each kind in a measurement line.
var kindLabels = map[MeasurementKind]string{
	KindCyclomatic: "cyclomatic complexity",
	KindCognitive:  "cognitive complexity",
	KindStatements: "statement count",
}

// ComplexityLimits are the cyclomatic, cognitive and statement limits a measurement is
// taken against. A non-positive limit falls back to the HISS-04 default.
type ComplexityLimits struct {
	MaxCyclomatic int
	MaxCognitive  int
	MaxStatements int
}

// WithDefaults completes every non-positive limit from the HISS-04 defaults.
func (l ComplexityLimits) WithDefaults() ComplexityLimits {
	if l.MaxCyclomatic <= 0 {
		l.MaxCyclomatic = DefaultMaxCyclomatic
	}
	if l.MaxCognitive <= 0 {
		l.MaxCognitive = DefaultMaxCognitive
	}
	if l.MaxStatements <= 0 {
		l.MaxStatements = DefaultMaxStatements
	}
	return l
}

// Measurement is one complexity value over its limit, located at the function it measures.
// Its Severity is always SeverityReport; the kind and severity are what every entry point
// prints and filters on, never the message text.
type Measurement struct {
	RuleID     string          `json:"rule_id"`
	FilePath   string          `json:"file_path"`
	LineNumber int             `json:"line_number"`
	Symbol     string          `json:"symbol,omitempty"`
	Kind       MeasurementKind `json:"kind"`
	Value      int             `json:"value"`
	Limit      int             `json:"limit"`
	Severity   Severity        `json:"severity"`
}

// Exceeded returns one unlocated measurement per metric over its limit, in the order
// cyclomatic, cognitive, statements. A value equal to its limit is within it.
func (m FuncMetrics) Exceeded(limits ComplexityLimits) []Measurement {
	limits = limits.WithDefaults()
	var out []Measurement
	for _, metric := range []struct {
		kind         MeasurementKind
		value, limit int
	}{
		{KindCyclomatic, m.Cyclomatic, limits.MaxCyclomatic},
		{KindCognitive, m.Cognitive, limits.MaxCognitive},
		{KindStatements, m.Statements, limits.MaxStatements},
	} {
		if metric.value > metric.limit {
			out = append(out, Measurement{
				RuleID: complexityRule, Kind: metric.kind, Value: metric.value,
				Limit: metric.limit, Severity: SeverityReport,
			})
		}
	}
	return out
}

// Measurements returns the unit's measurements over limits, located in file at the line
// the function starts on.
func (u FuncUnit) Measurements(fset *token.FileSet, file string, limits ComplexityLimits) []Measurement {
	out := u.Metrics.Exceeded(limits)
	line := fset.Position(u.Node.Pos()).Line
	for i := range out {
		out[i].FilePath, out[i].LineNumber, out[i].Symbol = file, line, u.Name
	}
	return out
}

// Detail describes the measurement without its location, as an editor diagnostic states it.
func (m Measurement) Detail() string {
	label := kindLabels[m.Kind]
	if label == "" {
		label = string(m.Kind)
	}
	return fmt.Sprintf("Function '%s' %s %d exceeds %d (%s: measured, not enforced)",
		m.Symbol, label, m.Value, m.Limit, m.Severity)
}

// String renders the measurement line every entry point prints.
func (m Measurement) String() string {
	return fmt.Sprintf("[%s] %s %s:%d %s", strings.ToUpper(string(m.Severity)),
		m.RuleID, m.FilePath, m.LineNumber, m.Detail())
}

// ComplexityReport is the complexity half of a scan: every measurement over a limit, and
// whether the scan cap cut the list short. It never affects a verdict.
type ComplexityReport struct {
	Measurements []Measurement `json:"measurements"`
	Truncated    bool          `json:"truncated,omitempty"`
}

// Summary states how many measurements the report holds, per kind and per function. A nil
// report states nothing.
func (c *ComplexityReport) Summary() string {
	if c == nil {
		return ""
	}
	perKind := make(map[MeasurementKind]int, len(kindLabels))
	functions := make(map[string]struct{}, len(c.Measurements))
	for _, m := range c.Measurements {
		perKind[m.Kind]++
		functions[fmt.Sprintf("%s:%d:%s", m.FilePath, m.LineNumber, m.Symbol)] = struct{}{}
	}
	summary := fmt.Sprintf("[%s] %s complexity measured, not enforced: %d measurements over limit in %d functions (cyclomatic %d, cognitive %d, statements %d)",
		strings.ToUpper(string(SeverityReport)), complexityRule, len(c.Measurements), len(functions),
		perKind[KindCyclomatic], perKind[KindCognitive], perKind[KindStatements])
	if c.Truncated {
		summary += "; the list stopped at the scan cap, so these counts are a lower bound"
	}
	return summary
}

// Lines renders the report as every entry point prints it: the summary, then one line per
// measurement up to MaxListedMeasurements, then a count of any left unlisted. A nil report,
// from a scan that never ran, renders no lines.
func (c *ComplexityReport) Lines() []string {
	if c == nil {
		return nil
	}
	listed := min(len(c.Measurements), MaxListedMeasurements)
	lines := make([]string, 0, listed+2)
	lines = append(lines, c.Summary())
	for i := 0; i < listed; i++ {
		lines = append(lines, c.Measurements[i].String())
	}
	if rest := len(c.Measurements) - listed; rest > 0 {
		lines = append(lines, fmt.Sprintf("[%s] %d more measurements are not listed; the JSON report carries all of them",
			strings.ToUpper(string(SeverityReport)), rest))
	}
	return lines
}

// recordMeasurement appends a measurement unless the scan cap is reached, in which case the
// complexity report is marked truncated. The infraction report is untouched either way.
func recordMeasurement(rep *ScanReport, m Measurement) {
	if rep.capLimit > 0 && len(rep.Complexity.Measurements) >= rep.capLimit {
		rep.Complexity.Truncated = true
		return
	}
	rep.Complexity.Measurements = append(rep.Complexity.Measurements, m)
}
