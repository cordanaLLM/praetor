// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
)

// BaselineFile is the repository-relative HISS-13 baseline snapshot path.
const BaselineFile = ".standards-baseline.json"

// ImageBaselineRecordOptions configures recording HISS-11 image reference findings into the baseline.
type ImageBaselineRecordOptions struct {
	Root          string
	BaselinePath  string
	AllowIncrease bool
	Reason        string
	Repository    string
	CommitSHA     string
	Exceptions    []config.Exception
	Today         time.Time
}

// ImageBaselineRecordResult reports the outcome of recording HISS-11 image reference findings.
type ImageBaselineRecordResult struct {
	Path     string
	Recorded int
	Previous *baseline.Baseline
	Next     *baseline.Baseline
}

// ImageFindingsToInfractions converts unpinned image reference findings into HISS-13 baseline entries.
func ImageFindingsToInfractions(findings []ImageRefFinding) []baseline.Infraction {
	out := make([]baseline.Infraction, 0, len(findings))
	for i := 0; i < len(findings) && i < maxScannedFiles; i++ {
		f := findings[i]
		out = append(out, baseline.Infraction{
			RuleID:      ImageRefRuleID,
			FilePath:    f.File,
			LineNumber:  f.Line,
			Message:     f.Reference,
			Fingerprint: f.Fingerprint,
		})
	}
	return out
}

// RecordImageRefBaseline scans root for unpinned container image references, filters out
// findings excused by live HISS-11 exceptions, and records the remainder in .standards-baseline.json
// while preserving entries of all other rules.
func RecordImageRefBaseline(ctx context.Context, opts ImageBaselineRecordOptions) (*ImageBaselineRecordResult, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}
	path := opts.BaselinePath
	if path == "" {
		path = filepath.Join(root, BaselineFile)
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	findings, err := ScanImageRefs(ctx, root)
	if err != nil {
		return nil, err
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleSupplyChain)
	unexcused := filterUnexcusedImageFindings(findings, entries, today)
	infractions := ImageFindingsToInfractions(unexcused)

	previous, err := baseline.LoadBaseline(path)
	if err != nil {
		return nil, err
	}
	allowIncrease := opts.AllowIncrease || strings.TrimSpace(opts.Reason) != ""
	next, err := baseline.Record(previous, infractions, baseline.RecordOptions{
		AllowIncrease: allowIncrease,
		Rationale:     opts.Reason,
		Repository:    opts.Repository,
		CommitSHA:     opts.CommitSHA,
		OwnedRules:    []string{ImageRefRuleID},
	})
	if err != nil {
		return nil, fmt.Errorf("refusing to record %s: %w (pass --allow-increase --reason=<why> to record a deliberate increase)", path, err)
	}
	if previous.Absent || !previous.SameDebt(next) || previous.CommitSHA != next.CommitSHA {
		if err := baseline.SaveBaseline(path, next); err != nil {
			return nil, err
		}
	}
	return &ImageBaselineRecordResult{
		Path:     path,
		Recorded: len(infractions),
		Previous: previous,
		Next:     next,
	}, nil
}

func filterUnexcusedImageFindings(findings []ImageRefFinding, entries []config.Exception, today time.Time) []ImageRefFinding {
	var out []ImageRefFinding
	for i := 0; i < len(findings) && i < maxScannedFiles; i++ {
		if isExcusedByLiveEntry(findings[i].File, entries, today) {
			continue
		}
		out = append(out, findings[i])
	}
	return out
}

func isExcusedByLiveEntry(file string, entries []config.Exception, today time.Time) bool {
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		if entries[i].Matches(file) && !entries[i].Expired(today) {
			return true
		}
	}
	return false
}
