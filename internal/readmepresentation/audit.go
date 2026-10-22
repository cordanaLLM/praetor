// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// MaxReportedFindings bounds the number of findings formatted in one report (HISS-02).
const MaxReportedFindings = 200

// MaxBaselineEntries bounds the number of README baseline entries scanned (HISS-02).
const MaxBaselineEntries = 1000

// Options holds the inputs for the README presentation audit.
type Options struct {
	Root     string
	Baseline *baseline.Baseline
	Enforce  bool
}

// ToBaseline converts README presentation findings (README-00..README-05) into baseline
// infractions anchored to README.md with deterministic occurrence fingerprints.
func ToBaseline(findings []Finding) []baseline.Infraction {
	n := len(findings)
	if n > MaxBaselineEntries {
		n = MaxBaselineEntries
	}
	infractions := make([]baseline.Infraction, 0, n)
	for i := 0; i < n; i++ {
		f := findings[i]
		file := f.File
		if file == "" {
			file = FileName
		}
		infractions = append(infractions, baseline.Infraction{
			RuleID:     f.RuleID,
			FilePath:   file,
			LineNumber: f.Line,
			Message:    f.Message,
			Anchor:     "file",
		})
	}
	baseline.AssignFingerprints(infractions)
	return infractions
}

// RecordBaseline builds a baseline from the current README presentation findings.
func RecordBaseline(previous *baseline.Baseline, findings []Finding, opts baseline.RecordOptions) (*baseline.Baseline, error) {
	if (previous == nil || previous.Absent) && !opts.AllowIncrease {
		opts.AllowIncrease = true
		if strings.TrimSpace(opts.Rationale) == "" {
			opts.Rationale = "initial README presentation baseline"
		}
	}
	return baseline.Record(previous, ToBaseline(findings), opts)
}

// ReadmeBaseline extracts README-00..README-05 entries from b and reports whether any were present.
func ReadmeBaseline(b *baseline.Baseline) (*baseline.Baseline, bool) {
	if b == nil || b.Absent {
		return &baseline.Baseline{Version: 1, Absent: true}, false
	}
	var entries []baseline.Infraction
	for i := 0; i < len(b.Infractions) && i < MaxBaselineEntries; i++ {
		if IsReadmeRule(b.Infractions[i].RuleID) {
			entries = append(entries, b.Infractions[i])
		}
	}
	if len(entries) == 0 {
		return &baseline.Baseline{Version: 1, Absent: true}, false
	}
	cp := *b
	cp.TotalInfractions = len(entries)
	cp.Infractions = entries
	return &cp, true
}

// WithoutReadmeBaseline returns a copy of b with README-00..README-05 entries removed so HISS
// invariant scans do not count README presentation baseline entries as stale HISS infractions.
func WithoutReadmeBaseline(b *baseline.Baseline) *baseline.Baseline {
	if b == nil || b.Absent || len(b.Infractions) == 0 {
		return b
	}
	hasReadme := false
	for i := 0; i < len(b.Infractions) && i < MaxBaselineEntries; i++ {
		if IsReadmeRule(b.Infractions[i].RuleID) {
			hasReadme = true
			break
		}
	}
	if !hasReadme {
		return b
	}
	filtered := make([]baseline.Infraction, 0, len(b.Infractions))
	for i := 0; i < len(b.Infractions) && i < MaxBaselineEntries; i++ {
		if !IsReadmeRule(b.Infractions[i].RuleID) {
			filtered = append(filtered, b.Infractions[i])
		}
	}
	cp := *b
	cp.TotalInfractions = len(filtered)
	cp.Infractions = filtered
	return &cp
}

// Audit runs the README presentation gate: checks README.md against rules README-00..README-05.
func Audit(ctx context.Context, opts Options) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + GateName + ": the audit requires a context")
	}
	findings, err := CheckFile(ctx, opts.Root, FileName)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", GateName, err)
	}
	readmeBase, hasBaseline := ReadmeBaseline(opts.Baseline)
	if hasBaseline || opts.Enforce {
		return judgeEnforced(findings, readmeBase)
	}
	return judgeReported(findings), nil
}

func judgeReported(findings []Finding) string {
	if len(findings) == 0 {
		return fmt.Sprintf("[PASS] %s: %s complies with the presentation standard (README-01..README-05).", GateName, FileName)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[REPORT] %s measured, not enforced: %d finding(s) in %s", GateName, len(findings), FileName)
	for i := 0; i < len(findings) && i < MaxReportedFindings; i++ {
		b.WriteByte('\n')
		b.WriteString(findings[i].ReportLine())
	}
	return b.String()
}

func judgeEnforced(findings []Finding, readmeBase *baseline.Baseline) (string, error) {
	ratchet := baseline.EvaluateRatchet(readmeBase, ToBaseline(findings), nil)
	if !ratchet.Passed {
		return "", errors.New(formatEnforcedFailure(findings, ratchet))
	}
	if len(findings) == 0 && readmeBase.TotalInfractions == 0 {
		return fmt.Sprintf("[PASS] %s: %s complies with the presentation standard (README-01..README-05).", GateName, FileName), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: %d active finding(s) within %d recorded in baseline.", GateName, ratchet.CurrentCount, readmeBase.TotalInfractions)
	for i := 0; i < len(findings) && i < MaxReportedFindings; i++ {
		b.WriteByte('\n')
		b.WriteString(findings[i].ReportLine())
	}
	if stale := ratchet.StaleNotice(); stale != "" {
		b.WriteString("\n[WARN] " + GateName + ": " + stale)
	}
	return b.String(), nil
}

func formatEnforcedFailure(findings []Finding, ratchet *baseline.RatchetResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[FAIL] %s: %s fails presentation standard (%d finding(s)):\n", GateName, FileName, len(findings))
	for i := 0; i < len(findings) && i < MaxReportedFindings; i++ {
		fmt.Fprintf(&b, "  - %s\n", findings[i].String())
	}
	b.WriteString(ratchet.Summary())
	return b.String()
}
