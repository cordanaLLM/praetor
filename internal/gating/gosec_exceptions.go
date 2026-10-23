// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/config"
)

type gosecExceptionRun struct {
	entries []config.Exception
	matched []bool
	excused []int
	today   time.Time
}

func loadGosecExceptionRun(ctx context.Context, root string) (*gosecExceptionRun, error) {
	entries, err := config.LoadExceptionsFor(ctx, root, config.ExceptionRuleGosec)
	if err != nil {
		return nil, fmt.Errorf("load gosec exceptions: %w", err)
	}
	return &gosecExceptionRun{entries: entries, matched: make([]bool, len(entries)), excused: make([]int, len(entries)), today: time.Now().UTC()}, nil
}

func gosecWithoutModules(ctx context.Context, cfg *stageConfig) (string, error) {
	run, err := loadGosecExceptionRun(ctx, cfg.repoDir)
	if err != nil {
		return "", err
	}
	if err := run.stale(); err != nil {
		return "", err
	}
	return "", notApplicable("no go.mod: Go security scanners skipped")
}

func runGosecModules(ctx context.Context, cfg *stageConfig) (string, error) {
	run, err := loadGosecExceptionRun(ctx, cfg.repoDir)
	if err != nil {
		return "", err
	}
	cfg.gosecExceptions = run
	results, err := runGoModules(ctx, cfg, runGoSecurityModule)
	if err != nil {
		return "", err
	}
	message := combineSecurityMessages(moduleResultsMessage(results), cfg.gosecExceptions.summary())
	if runErr := moduleResultsError("Go security", results); runErr != nil {
		return message, fmt.Errorf("%w; results: %s", runErr, message)
	}
	if err := cfg.gosecExceptions.stale(); err != nil {
		return message, err
	}
	return message, nil
}

func (r *gosecExceptionRun) filter(findings []baseline.Infraction) []baseline.Infraction {
	if r == nil {
		return findings
	}
	remaining := make([]baseline.Infraction, 0, len(findings))
	for i := 0; i < len(findings) && i < maxGosecIssues; i++ {
		if !r.excuses(findings[i]) {
			remaining = append(remaining, findings[i])
		}
	}
	return remaining
}

func (r *gosecExceptionRun) excuses(finding baseline.Infraction) bool {
	excused := false
	for i := 0; i < len(r.entries) && i < config.MaxExceptions; i++ {
		entry := r.entries[i]
		if !entry.MatchesGosec(finding.FilePath, finding.RuleID) {
			continue
		}
		r.matched[i] = true
		if !entry.Expired(r.today) {
			r.excused[i]++
			excused = true
		}
	}
	return excused
}

func (r *gosecExceptionRun) stale() error {
	for i := 0; i < len(r.entries) && i < config.MaxExceptions; i++ {
		if !r.matched[i] {
			return fmt.Errorf("stale gosec exception[%d] (%s, %s): no matching finding; remove the entry", i, r.entries[i].Path, strings.Join(r.entries[i].GosecRules, ", "))
		}
	}
	return nil
}

func (r *gosecExceptionRun) summary() string {
	lines := make([]string, 0, len(r.entries))
	for i := 0; i < len(r.entries) && i < config.MaxExceptions; i++ {
		if r.excused[i] == 0 {
			continue
		}
		entry := r.entries[i]
		lines = append(lines, fmt.Sprintf("%d gosec findings excused by exception[%d] (%s, %s): %s; until %s", r.excused[i], i, entry.Path, strings.Join(entry.GosecRules, ", "), entry.Reason, entry.Expires))
	}
	return strings.Join(lines, "; ")
}
