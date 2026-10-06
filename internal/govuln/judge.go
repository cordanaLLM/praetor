// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

// Level is how deep govulncheck traced an advisory into the module.
type Level string

const (
	// LevelModule: a module of the advisory is required, and none of its vulnerable packages is
	// imported.
	LevelModule Level = "module"
	// LevelPackage: a vulnerable package is imported, and none of its vulnerable symbols is
	// called.
	LevelPackage Level = "package"
	// LevelSymbol: a vulnerable symbol is called.
	LevelSymbol Level = "symbol"
)

// depth orders the levels: module, package, symbol.
func (l Level) depth() int {
	return slices.Index([]Level{LevelModule, LevelPackage, LevelSymbol}, l)
}

// levelOf is the level of a finding: what its first frame names.
func levelOf(f rawFinding) Level {
	top := f.Trace[0]
	switch {
	case top.Function != "":
		return LevelSymbol
	case top.Package != "":
		return LevelPackage
	}
	return LevelModule
}

// Verdict is the gate's judgement of one advisory, on its deepest finding.
type Verdict struct {
	OSV          string
	Level        Level
	Where        string // the module, package or symbol the finding names
	FixedVersion string // empty when no fixed version exists
	Covered      bool   // a current not_affected statement covers it; otherwise it fails the gate
	Reason       string
}

// String renders the verdict as one report line.
func (v Verdict) String() string {
	return v.OSV + ": " + v.Reason
}

// Report is the outcome of one completed scan.
type Report struct {
	Scanner  string // name and version govulncheck reported for itself
	VEXPath  string // the repository path of the OpenVEX document
	VEXFound bool   // false when no file exists at VEXPath
	Verdicts []Verdict
	// Unused names the advisories of not_affected statements that no finding matched, so a
	// statement outliving its advisory is removed instead of waiving the next one.
	Unused []string
}

// Failed reports whether an advisory fails the gate.
func (r *Report) Failed() bool {
	return slices.ContainsFunc(r.Verdicts, func(v Verdict) bool { return !v.Covered })
}

// Lines renders the verdicts the gate fails on (covered false) or passes with (covered true).
func (r *Report) Lines(covered bool) []string {
	lines := make([]string, 0, len(r.Verdicts))
	for i := 0; i < len(r.Verdicts); i++ {
		if r.Verdicts[i].Covered == covered {
			lines = append(lines, r.Verdicts[i].String())
		}
	}
	return lines
}

// UnusedLines renders one note per unused statement.
func (r *Report) UnusedLines() []string {
	lines := make([]string, 0, len(r.Unused))
	for i := 0; i < len(r.Unused); i++ {
		lines = append(lines, fmt.Sprintf("the not_affected statement for %s in %s matches no finding; remove it once the advisory no longer applies",
			r.Unused[i], r.VEXPath))
	}
	return lines
}

// Summary is the one-line outcome: the scanner that ran and how many advisories failed and were
// covered.
func (r *Report) Summary() string {
	failing := len(r.Lines(false))
	return fmt.Sprintf("%s, symbol scan: %d advisories present, %d failing, %d covered by %s",
		r.Scanner, len(r.Verdicts), failing, len(r.Verdicts)-failing, r.VEXPath)
}

// judge gives every advisory of the scan its verdict, in advisory order.
func judge(scan *scanResult, vex *vexIndex, now time.Time) *Report {
	report := &Report{Scanner: scan.scanner, VEXPath: vex.path, VEXFound: vex.found}
	ids := make([]string, 0, len(scan.findings))
	for id := range scan.findings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	used := map[int]bool{}
	for i := 0; i < len(ids); i++ {
		names := append([]string{ids[i]}, scan.aliases[ids[i]]...)
		st, tie := vex.latest(names)
		if st != nil {
			used[st.index] = true
		}
		report.Verdicts = append(report.Verdicts, assess(scan.findings[ids[i]], st, tie, vex, now))
	}
	for i := 0; i < len(vex.statements); i++ {
		if vex.statements[i].status == statusNotAffect && !used[vex.statements[i].index] {
			report.Unused = append(report.Unused, vex.statements[i].names[0])
		}
	}
	return report
}

// assess judges one advisory on its deepest finding f and the statement st that speaks for it.
func assess(f rawFinding, st *statement, tie bool, vex *vexIndex, now time.Time) Verdict {
	v := Verdict{OSV: f.OSV, Level: levelOf(f), Where: where(f), FixedVersion: f.FixedVersion}
	if v.Level == LevelSymbol {
		v.Reason = fmt.Sprintf("%s is called%s; no VEX statement covers a called symbol: update %s or stop calling it",
			v.Where, fixedClause(f.FixedVersion), f.Trace[0].Module)
		return v
	}
	finding := fmt.Sprintf("%s %s is %s but not called", v.Level, v.Where, presence(v.Level))
	v.Reason = statementProblem(finding, v.Level, st, tie, vex, now)
	if v.Reason == "" {
		v.Covered = true
		v.Reason = fmt.Sprintf("%s; %s (%s) in %s, reviewed %s", finding, statusNotAffect, st.justification,
			vex.path, st.reviewed.UTC().Format(reviewDateLayout))
	}
	return v
}

// statementProblem says why st does not cover a package- or module-level finding, or returns ""
// when it does.
func statementProblem(finding string, level Level, st *statement, tie bool, vex *vexIndex, now time.Time) string {
	switch {
	case tie:
		return fmt.Sprintf("%s, and two statements in %s name it with the same review time; keep one", finding, vex.path)
	case st == nil && !vex.found:
		return fmt.Sprintf("%s, and %s does not exist to hold a %s statement for it", finding, vex.path, statusNotAffect)
	case st == nil:
		return fmt.Sprintf("%s, and %s holds no %s statement for it", finding, vex.path, statusNotAffect)
	case st.status != statusNotAffect:
		return fmt.Sprintf("%s, and the latest statement in %s says %s, not %s", finding, vex.path, st.status, statusNotAffect)
	case now.Sub(st.reviewed) > MaxStatementAge:
		return fmt.Sprintf("%s, and its %s statement in %s was last reviewed %s, more than %d days ago; review it and update its last_updated",
			finding, statusNotAffect, vex.path, st.reviewed.UTC().Format(reviewDateLayout), int(MaxStatementAge.Hours()/24))
	case level == LevelPackage && slices.Contains(absentJustifications, st.justification):
		return fmt.Sprintf("%s, but %s justifies it as %s, which covers a module-level finding only; record what keeps it from being called",
			finding, vex.path, st.justification)
	}
	return ""
}

// where names what a finding's first frame names: the called symbol, the imported package or the
// required module.
func where(f rawFinding) string {
	top := f.Trace[0]
	switch levelOf(f) {
	case LevelSymbol:
		symbol := top.Function
		if top.Receiver != "" {
			symbol = top.Receiver + "." + symbol
		}
		return top.Package + "." + symbol
	case LevelPackage:
		return top.Package
	}
	if top.Version == "" {
		return top.Module
	}
	return top.Module + "@" + top.Version
}

// presence says how a package- or module-level finding is present in the build.
func presence(level Level) string {
	if level == LevelPackage {
		return "imported"
	}
	return "required"
}

// fixedClause names the fixed version, or says there is none.
func fixedClause(fixed string) string {
	if fixed == "" {
		return " (no fixed version)"
	}
	return " (fixed in " + fixed + ")"
}

// FailureSummary joins the failing verdicts into one message for a stage report.
func (r *Report) FailureSummary() string {
	return strings.Join(r.Lines(false), "; ")
}
