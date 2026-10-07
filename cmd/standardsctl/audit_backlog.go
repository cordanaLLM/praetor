// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/backlogcap"
	"github.com/cordanaLLM/praetor/internal/config"
)

// maxBacklogFindingLines bounds the findings one backlog report prints per category before it
// counts the rest (HISS-02); a batch file lists them all.
const maxBacklogFindingLines = 3

// auditBacklogCaps is the audit's backlog cap gate (#792). It prints every capped category
// with its count and cap and fails when a category whose action is gate is over its cap, or
// cannot be counted. A policy that caps nothing prints nothing: no cap declared means no
// bound. The ledgers are the private .workingdir of the audited checkout; a checkout without
// one, such as a CI clone, counts each absent ledger as empty and says so.
func auditBacklogCaps(ctx context.Context, rootDir string, effective *config.EffectivePolicy) error {
	report, err := backlogcap.Evaluate(ctx, rootDir, effective)
	if err != nil {
		return fmt.Errorf("[FAIL] Backlog cap audit failed: %w", err)
	}
	if len(report.Categories) == 0 {
		return nil
	}
	printBacklogLines(report, "[INFO] Backlog cap ", "[WARN] Backlog cap ")
	if err := report.Gate(); err != nil {
		return fmt.Errorf("[FAIL] %w", err)
	}
	fmt.Println("[PASS] Backlog caps: no category with action gate is over its cap.")
	return nil
}

// printBacklogLines prints each category's line under prefix, then a bounded summary of its
// findings under findingPrefix.
func printBacklogLines(report *backlogcap.Report, prefix, findingPrefix string) {
	for i := range report.Categories {
		category := &report.Categories[i]
		fmt.Println(prefix + category.Line())
		for j := 0; j < len(category.Findings) && j < maxBacklogFindingLines; j++ {
			fmt.Println(findingPrefix + category.Name + ": " + category.Findings[j])
		}
		if extra := len(category.Findings) - maxBacklogFindingLines; extra > 0 {
			fmt.Printf("%s%s: %d more findings\n", findingPrefix, category.Name, extra)
		}
	}
}
