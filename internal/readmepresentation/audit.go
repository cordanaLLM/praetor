// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

// MaxReportedFindings bounds the number of findings formatted in one report (HISS-02).
const MaxReportedFindings = 200

var lineRefRe = regexp.MustCompile(`\bline [0-9]+\b`)

// Options holds the inputs for the README presentation audit.
type Options struct {
	Root     string
	Baseline *baseline.Baseline
	Enforce  bool
}

func findingAnchor(msg string) string {
	norm := strings.TrimSpace(lineRefRe.ReplaceAllString(msg, "line #"))
	if norm == "" {
		return "file"
	}
	sum := sha256.Sum256([]byte(norm))
	return fmt.Sprintf("file:%x", sum[:6])
}

// ToBaseline converts README presentation findings (README-00..README-05) into baseline
// infractions anchored to README.md with deterministic occurrence fingerprints.
func ToBaseline(findings []Finding) []baseline.Infraction {
	n := len(findings)
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
			Anchor:     findingAnchor(f.Message),
		})
	}
	baseline.AssignFingerprints(infractions)
	return infractions
}

// RecordBaseline builds a baseline from the current README presentation findings and any
// HISS infractions provided by the caller.
func RecordBaseline(previous *baseline.Baseline, findings []Finding, opts baseline.RecordOptions, hissInfractions ...baseline.Infraction) (*baseline.Baseline, error) {
	if previous == nil && !opts.AllowIncrease {
		opts.AllowIncrease = true
		if strings.TrimSpace(opts.Rationale) == "" {
			opts.Rationale = "initial README presentation baseline"
		}
	}
	readmeInf := ToBaseline(findings)
	combined := make([]baseline.Infraction, 0, len(hissInfractions)+len(readmeInf))
	combined = append(combined, hissInfractions...)
	combined = append(combined, readmeInf...)
	return baseline.Record(previous, combined, opts)
}

func readmeExists(rootDir string) bool {
	_, err := os.Lstat(filepath.Join(rootDir, FileName))
	return err == nil
}

// ScanBaselineFindings scans rootDir/README.md for baseline recording or verification when
// previous already carries README entries or when includePresent is set and README.md exists.
func ScanBaselineFindings(ctx context.Context, rootDir string, previous *baseline.Baseline, includePresent bool) ([]Finding, error) {
	_, hasReadme := ReadmeBaseline(previous)
	if !hasReadme && (!includePresent || !readmeExists(rootDir)) {
		return nil, nil
	}
	return CheckFile(ctx, rootDir, FileName)
}

// ReadmeBaseline extracts README-00..README-05 entries from b and reports whether any were present.
func ReadmeBaseline(b *baseline.Baseline) (*baseline.Baseline, bool) {
	if b == nil || b.Absent {
		return &baseline.Baseline{Version: 1, Absent: true}, false
	}
	var entries []baseline.Infraction
	for i := 0; i < len(b.Infractions); i++ {
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
	if b == nil || len(b.Infractions) == 0 {
		return b
	}
	hasReadme := false
	filtered := make([]baseline.Infraction, 0, len(b.Infractions))
	for i := 0; i < len(b.Infractions); i++ {
		if IsReadmeRule(b.Infractions[i].RuleID) {
			hasReadme = true
			continue
		}
		filtered = append(filtered, b.Infractions[i])
	}
	if !hasReadme {
		return b
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
	findings, infos, err := CheckFileWithInfo(ctx, opts.Root, FileName)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", GateName, err)
	}
	readmeBase, hasBaseline := ReadmeBaseline(opts.Baseline)
	if hasBaseline || opts.Enforce {
		out, err := judgeEnforced(findings, readmeBase)
		if err != nil {
			return "", err
		}
		return appendInfoLines(out, infos), nil
	}
	return appendInfoLines(judgeReported(findings), infos), nil
}

func appendInfoLines(base string, infos []Finding) string {
	if len(infos) == 0 {
		return base
	}
	var b strings.Builder
	b.WriteString(base)
	for i := 0; i < len(infos) && i < MaxReportedFindings; i++ {
		b.WriteByte('\n')
		b.WriteString(infos[i].InfoLine())
	}
	return b.String()
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
