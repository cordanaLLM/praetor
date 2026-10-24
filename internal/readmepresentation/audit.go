// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
	MaxStale *int
}

func findingAnchor(file, ruleID, msg string) string {
	rel := baseline.NormalizePath(file)
	if rel == "" {
		rel = FileName
	}
	rule := strings.TrimSpace(ruleID)
	norm := strings.TrimSpace(lineRefRe.ReplaceAllString(msg, "line #"))
	sum := sha256.Sum256([]byte(rel + "\x00" + rule + "\x00" + norm))
	return fmt.Sprintf("file:%x", sum[:6])
}

// ToBaseline converts README presentation findings (README-00..README-05) into baseline
// infractions anchored to README.md with deterministic occurrence fingerprints.
func ToBaseline(findings []Finding) []baseline.Infraction {
	n := len(findings)
	infractions := make([]baseline.Infraction, 0, n)
	for i := 0; i < n; i++ {
		f := findings[i]
		file := baseline.NormalizePath(f.File)
		if file == "" {
			file = FileName
		}
		infractions = append(infractions, baseline.Infraction{
			RuleID:     f.RuleID,
			FilePath:   file,
			LineNumber: f.Line,
			Message:    f.Message,
			Anchor:     findingAnchor(file, f.RuleID, f.Message),
		})
	}
	baseline.AssignFingerprints(infractions)
	return infractions
}

// NotRecorded describes an absent, unenforced snapshot without claiming a scan passed.
const NotRecorded = ".readme-baseline.json: not recorded (no README presentation baseline; enforcement off)"

// NotRecordedClean names a clean enforced scan that did not create a snapshot.
const NotRecordedClean = ".readme-baseline.json: not recorded (no README presentation baseline; presentation scan clean)"

// ScanBaselineFindings scans rootDir/README.md for baseline recording or verification when
// the README snapshot already exists or enforce is set.
func ScanBaselineFindings(ctx context.Context, rootDir string, previous *baseline.Baseline, enforce bool) ([]Finding, error) {
	if (previous == nil || previous.Absent) && !enforce {
		return nil, nil
	}
	return CheckFile(ctx, rootDir, FileName)
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
	readmeBase := opts.Baseline
	hasBaseline := readmeBase != nil && !readmeBase.Absent
	if hasBaseline || opts.Enforce {
		if readmeBase == nil {
			readmeBase = &baseline.Baseline{Absent: true}
		}
		out, err := judgeEnforced(findings, readmeBase, opts.MaxStale)
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

func judgeEnforced(findings []Finding, readmeBase *baseline.Baseline, maxStale *int) (string, error) {
	ratchet := baseline.EvaluateRatchet(readmeBase, ToBaseline(findings), nil)
	ratchet.Subject = "README presentation findings"
	if !ratchet.Passed {
		return "", errors.New(formatEnforcedFailure(findings, ratchet))
	}
	if len(findings) == 0 && readmeBase.Count() == 0 {
		return emptyReadmeVerdict(readmeBase), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: %d active finding(s) within %d recorded in .readme-baseline.json.", GateName, ratchet.CurrentCount, readmeBase.Count())
	for i := 0; i < len(findings) && i < MaxReportedFindings; i++ {
		b.WriteByte('\n')
		b.WriteString(findings[i].BaselinedLine())
	}
	if stale := ratchet.StaleNotice(); stale != "" {
		if maxStale != nil && *maxStale >= 0 && ratchet.Stale > *maxStale {
			return "", fmt.Errorf("[FAIL] %s: %s; --max-stale-baseline-entries=%d allows at most %d",
				GateName, stale, *maxStale, *maxStale)
		}
		b.WriteString("\n[WARN] " + GateName + ": " + stale)
	}
	return b.String(), nil
}

func emptyReadmeVerdict(readmeBase *baseline.Baseline) string {
	if readmeBase.Absent {
		return fmt.Sprintf("[PASS] %s: %s complies with the presentation standard (README-01..README-05).\n[INFO] %s", GateName, FileName, NotRecordedClean)
	}
	return fmt.Sprintf("[PASS] %s: %s complies with the presentation standard (README-01..README-05); 0 active finding(s) within 0 recorded in .readme-baseline.json.", GateName, FileName)
}

func formatEnforcedFailure(findings []Finding, ratchet *baseline.RatchetResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[FAIL] %s: %s fails presentation standard (%d finding(s), .readme-baseline.json):\n", GateName, FileName, len(findings))
	for i := 0; i < len(findings) && i < MaxReportedFindings; i++ {
		fmt.Fprintf(&b, "  - %s\n", findings[i].String())
	}
	b.WriteString(ratchet.Summary())
	return b.String()
}
