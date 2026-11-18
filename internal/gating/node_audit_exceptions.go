// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// judgeNodeAdvisories validates configured ignores before judging any npm or pnpm report shape.
func judgeNodeAdvisories(ctx context.Context, root, manager, out string) (bool, error) {
	if manager != nodeManagerNpm && manager != nodeManagerPnpm {
		return false, nil
	}
	entries, err := config.LoadExceptionsFor(ctx, root, config.ExceptionRuleNodeAudit)
	if err != nil {
		return false, fmt.Errorf("load Node audit exceptions from .standards.yaml: %w", err)
	}
	ignored, err := nodeAuditIgnored(ctx, root, manager)
	if err != nil {
		return false, err
	}
	if err := checkIgnoredNodeAdvisories(entries, ignored); err != nil {
		return false, err
	}
	report, err := structuredNodeAudit(out)
	if err != nil || report == nil {
		return false, err
	}
	return judgeStructuredNodeAdvisories(*report, entries, ignored)
}

// judgeStructuredNodeAdvisories checks listed findings and gaps after configured ignores pass.
func judgeStructuredNodeAdvisories(report npmAuditReport, entries []config.Exception, ignored []string) (bool, error) {
	findings, err := listedNodeFindings(report)
	if err != nil {
		return false, err
	}
	highGap := max(0, report.Metadata.Vulnerabilities.High-findings.high)
	critGap := max(0, report.Metadata.Vulnerabilities.Critical-findings.critical)
	unlisted := unlistedNodeIgnoreCount(findings.seen, ignored)
	if highGap > unlisted || critGap > unlisted-highGap {
		return false, fmt.Errorf("%d high or critical findings counted but not named; %d configured ignores can explain unidentified advisories", uint64(highGap)+uint64(critGap), unlisted)
	}
	if err := checkListedNodeAdvisories(entries, findings.ids); err != nil {
		return false, err
	}
	return len(findings.ids) > 0 || findings.high+findings.critical+highGap+critGap > 0, nil
}

// unlistedNodeIgnoreCount counts configured pnpm ignores absent from every listed advisory or vulnerability.
func unlistedNodeIgnoreCount(listed map[string]string, ignored []string) int {
	unlisted := make(map[string]struct{}, len(ignored))
	for i := 0; i < len(ignored) && i < maxNodeAdvisories; i++ {
		if _, ok := listed[ignored[i]]; !ok {
			unlisted[ignored[i]] = struct{}{}
		}
	}
	return len(unlisted)
}

// structuredNodeAudit identifies supported maps and refuses failed structured reports.
func structuredNodeAudit(out string) (*npmAuditReport, error) {
	if len(out) > 16<<20 {
		return nil, fmt.Errorf("node audit report exceeds 16 MiB")
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		return nil, nil
	}
	if err := strictjson.Validate([]byte(out), strictjson.Options{MaxBytes: 16 << 20, MaxDepth: 64}); err != nil {
		return nil, fmt.Errorf("parse Node audit: %w", err)
	}
	var report npmAuditReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		return nil, fmt.Errorf("parse Node audit: %w", err)
	}
	if len(report.Error) > 0 && string(report.Error) != "null" {
		return nil, fmt.Errorf("node audit report contains a command error")
	}
	if report.Advisories == nil && report.Vulnerabilities == nil {
		return nil, nil
	}
	return &report, nil
}

// checkListedNodeAdvisories rejects each listed high or critical advisory without a live entry.
func checkListedNodeAdvisories(entries []config.Exception, ids []string) error {
	for i := 0; i < len(ids) && i < maxNodeAdvisories; i++ {
		if err := nodeAdvisoryException(entries, ids[i]); err != nil {
			return fmt.Errorf("listed high or critical advisory: %w", err)
		}
	}
	return nil
}

// checkIgnoredNodeAdvisories requires a live entry for every configured ignore on every report path.
func checkIgnoredNodeAdvisories(entries []config.Exception, ignored []string) error {
	missing := make([]string, 0, len(ignored))
	for i := 0; i < len(ignored) && i < maxNodeAdvisories; i++ {
		live, expired := config.NodeAuditExceptionFor(entries, ignored[i], time.Now().UTC())
		if live != nil {
			continue
		}
		if expired != nil {
			return nodeAdvisoryException(entries, ignored[i])
		}
		missing = append(missing, ignored[i])
	}
	if len(missing) > 0 {
		return fmt.Errorf("configured ignored advisories; no exception in .standards.yaml covers %s", strings.Join(missing, ", "))
	}
	return nil
}

// nodeAdvisoryException distinguishes a missing GHSA waiver from a dated but expired waiver.
func nodeAdvisoryException(entries []config.Exception, id string) error {
	live, expired := config.NodeAuditExceptionFor(entries, id, time.Now().UTC())
	if live != nil {
		return nil
	}
	if expired != nil {
		return fmt.Errorf("%s exception in .standards.yaml expired on %s", id, expired.Expires)
	}
	return fmt.Errorf("%s has no unexpired exception in .standards.yaml", id)
}

// uniqueNodeIgnoreIDs validates and deduplicates a bounded configuration list before matching.
func uniqueNodeIgnoreIDs(ids []string) ([]string, error) {
	if len(ids) > maxNodeAdvisories {
		return nil, fmt.Errorf("node audit ignore list exceeds %d entries", maxNodeAdvisories)
	}
	for i := 0; i < len(ids) && i < maxNodeAdvisories; i++ {
		if !config.ValidGHSA(ids[i]) {
			return nil, fmt.Errorf("node audit ignore %q is not a GHSA id; .standards.yaml requires exact GHSA exceptions", ids[i])
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids), nil
}
