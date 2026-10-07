// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/backlogcap"
	"github.com/cordanaLLM/praetor/internal/config"
)

// maxBacklogFindingLines bounds the findings one backlog report prints per category before it
// counts the rest (HISS-02); a batch file lists them all.
const maxBacklogFindingLines = 3

// auditBacklogCaps is the audit's backlog cap gate (#792). It prints every capped category
// with its count and cap and fails when a category whose action is gate is over its cap, or
// cannot be counted because praetor has no reader for it or its ledger could not be read. A
// category with action report or batch whose ledger cannot be read is reported and never
// fails the audit. A policy that caps nothing prints nothing: no cap declared means no bound.
//
// The ledgers are the private .workingdir of the audited checkout. In a checkout without them,
// such as a CI clone, a gated category is not counted, never counted as zero: it prints SKIP
// with the reason, and the summary is SKIP. PASS needs every gated category counted and within
// its cap (HISS-21: a gate that cannot run is not a passing gate).
func auditBacklogCaps(ctx context.Context, rootDir string, effective *config.EffectivePolicy) error {
	report, err := backlogcap.Evaluate(ctx, rootDir, effective)
	if err != nil {
		return fmt.Errorf("[FAIL] Backlog cap audit failed: %w", err)
	}
	if len(report.Categories) == 0 {
		return nil
	}
	printBacklogLines(report, auditBacklogPrefix, "[WARN] Backlog cap ")
	if err := report.Gate(); err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	switch skipped := report.Skipped(); {
	case len(skipped) > 0:
		fmt.Printf("[SKIP] Backlog caps: the gate on %s did not run: the ledger is not present in this checkout.\n",
			strings.Join(skipped, ", "))
	case report.Gated() == 0:
		fmt.Println("[INFO] Backlog caps: no category declares action gate; nothing is gated.")
	default:
		fmt.Println("[PASS] Backlog caps: every category with action gate was counted and is within its cap.")
	}
	return nil
}

// auditBacklogPrefix marks a category line in the audit: SKIP for a gated category whose
// ledger is absent, WARN for one that could not be counted, INFO otherwise.
func auditBacklogPrefix(category *backlogcap.Category) string {
	switch state := category.State(); {
	case state == backlogcap.StateAbsent && category.Gated():
		return "[SKIP] Backlog cap "
	case state == backlogcap.StateNotCounted:
		return "[WARN] Backlog cap "
	}
	return "[INFO] Backlog cap "
}

// printBacklogLines prints each category's line under the prefix it maps the category to,
// then a bounded summary of its findings under findingPrefix.
func printBacklogLines(report *backlogcap.Report, prefix func(*backlogcap.Category) string, findingPrefix string) {
	for i := range report.Categories {
		category := &report.Categories[i]
		fmt.Println(prefix(category) + category.Line())
		for j := 0; j < len(category.Findings) && j < maxBacklogFindingLines; j++ {
			fmt.Println(findingPrefix + category.Name + ": " + category.Findings[j])
		}
		if extra := len(category.Findings) - maxBacklogFindingLines; extra > 0 {
			fmt.Printf("%s%s: %d more findings\n", findingPrefix, category.Name, extra)
		}
	}
}

// fixedBacklogPrefix maps every category to prefix.
func fixedBacklogPrefix(prefix string) func(*backlogcap.Category) string {
	return func(*backlogcap.Category) string { return prefix }
}
