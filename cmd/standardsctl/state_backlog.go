// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/backlogcap"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/state"
)

// stateCapsPrefix and stateCapsIndent align the backlog cap lines with the rest of
// `state status`.
const (
	stateCapsPrefix = "  Backlog Cap:       "
	stateCapsIndent = "                     "
)

// evaluateBacklogCaps resolves the repository's backlog caps from the sources the audit reads
// (registerPolicySourceFlags) and counts each capped category. A repository without
// .standards.yaml has no caps; one without .standards.lock resolves without pinned profiles,
// and the notice says so.
func evaluateBacklogCaps(ctx context.Context, dir string, policy config.EffectiveOptions) (*backlogcap.Report, string, error) {
	policy.Root, policy.ManifestPath = dir, ""
	effective, notice, err := config.LoadUnadoptedEffectivePolicyContext(ctx, policy)
	if err != nil {
		return nil, "", fmt.Errorf("resolve backlog caps: %w", err)
	}
	report, err := backlogcap.Evaluate(ctx, dir, effective)
	return report, notice, err
}

// printStateBacklogCaps appends the backlog caps to `state status`. Nothing is printed while no
// cap is declared, so the status of an uncapped repository is unchanged. A category that
// cannot be counted, because a ledger is absent or unreadable, prints as not counted with the
// reason. A policy that does not resolve prints as unresolved when a layer declares a backlog
// section, or when that cannot be told (config.DeclaresBacklogContext): the caps are then
// unknown, not absent. With no backlog section anywhere it prints nothing, as without caps.
func printStateBacklogCaps(ctx context.Context, dir string, policy config.EffectiveOptions) {
	report, _, err := evaluateBacklogCaps(ctx, dir, policy)
	if err != nil {
		policy.Root, policy.ManifestPath = dir, ""
		if declared, probeErr := config.DeclaresBacklogContext(ctx, policy); probeErr != nil || declared {
			fmt.Printf("%sunresolved: %v\n", stateCapsPrefix, err)
		}
		return
	}
	printBacklogLines(report, fixedBacklogPrefix(stateCapsPrefix), stateCapsIndent)
	for i := range report.Categories {
		category := &report.Categories[i]
		if category.State() == backlogcap.StateOver && category.Cap.EffectiveAction().Includes(config.BacklogBatch) {
			fmt.Printf("%s%s: run 'praetorctl state batch' to write its batch\n", stateCapsIndent, category.Name)
		}
	}
}

// runStateBatch writes one batch file per category over its cap whose action is batch or gate.
func runStateBatch(args []string) error {
	var policy config.EffectiveOptions
	var date *string
	dirFlag, rest, err := stateArgs("state batch", args, func(fs *flag.FlagSet) {
		date = fs.String("date", time.Now().UTC().Format(time.DateOnly), "Date (YYYY-MM-DD) that names and heads each batch file")
		registerPolicySourceFlags(fs, &policy, "repository root")
	})
	if err != nil {
		return err
	}
	dir := stateDir(dirFlag, rest, 0)
	ctx, cancel := context.WithTimeout(rootContext(), stateCommandTimeout)
	defer cancel()
	report, notice, err := evaluateBacklogCaps(ctx, dir, policy)
	if err != nil {
		return fmt.Errorf("state batch failed: %w", err)
	}
	if notice != "" {
		fmt.Printf("[INFO] %s\n", notice)
	}
	if len(report.Categories) == 0 {
		fmt.Println("state batch: no backlog cap is declared; nothing written")
		return nil
	}
	printBacklogLines(report, fixedBacklogPrefix("  "), "    ")
	return withLedgerIgnore(ctx, dir, func() error {
		written, err := backlogcap.WriteBatches(ctx, dir, report, *date)
		for _, path := range written {
			fmt.Printf("Wrote %s\n", path)
		}
		if err == nil && len(written) == 0 {
			fmt.Println("state batch: no category with action batch or gate is over its cap; nothing written")
		}
		return err
	})
}

// runStateBugKind labels one bug ledger row as a defect or as scope.
func runStateBugKind(args []string) error {
	dirFlag, rest, err := stateArgs("state bug kind", args, nil)
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: praetorctl state bug kind <id> <defect|scope> [--dir=.]")
	}
	id, kind, dir := rest[0], rest[1], stateDir(dirFlag, rest, 2)
	ctx, cancel := context.WithTimeout(rootContext(), stateCommandTimeout)
	defer cancel()
	return withLedgerIgnore(ctx, dir, func() error {
		if err := state.SetBugKind(dir, id, kind); err != nil {
			return err
		}
		fmt.Printf("Labelled bug %s as %s\n", id, kind)
		return nil
	})
}
