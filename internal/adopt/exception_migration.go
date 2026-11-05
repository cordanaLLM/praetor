// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// adoptionExceptionMigration selects exactly the manifest rule kinds adoption renders.
// The shared planner owns category, blame, fallback and expiry resolution.
func adoptionExceptionMigration(ctx context.Context, full string, data []byte) ([]byte, []config.ExceptionMigration, error) {
	updated, proposals, err := config.PlanExceptionMigrationForRules(ctx, full, data, time.Now(),
		[]string{config.ExceptionRuleSupplyChain, config.ExceptionRuleDeclaredLinters})
	if err != nil {
		return nil, nil, fmt.Errorf("plan adoption exception migration: %w", err)
	}
	return updated, proposals, nil
}

// adoptionManifestSnapshot lets preparation and preflight validate the proposed manifest,
// without publishing it before all preflight checks have passed.
func adoptionManifestSnapshot(ctx context.Context, root string) (string, []byte, []config.ExceptionMigration, error) {
	full, err := repoFile(root, manifestFile)
	if err != nil {
		return "", nil, nil, err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read adoption exception manifest: %w", err)
	}
	if !exists {
		return "", nil, nil, nil
	}
	updated, proposals, err := adoptionExceptionMigration(ctx, full, data)
	if err != nil {
		return "", nil, nil, err
	}
	if len(proposals) == 0 {
		return "", nil, nil, nil
	}
	return full, updated, proposals, nil
}

func reportUnresolvedAdoptionExceptions(report *AdoptReport, proposals []config.ExceptionMigration) error {
	unresolved := 0
	for i := 0; i < len(proposals) && i < config.MaxExceptions; i++ {
		proposal := proposals[i]
		if proposal.Unresolved != "" {
			report.addWarning("unresolved exception %s %s: %s (%s); entry unchanged",
				proposal.Entry.Rule, proposal.Entry.Target(), proposal.Unresolved, proposal.Source)
			unresolved++
		}
	}
	if unresolved > 0 {
		return fmt.Errorf("adoption exception migration: %d unresolved entries", unresolved)
	}
	return nil
}

func adoptionExceptionMigrationNote(proposals []config.ExceptionMigration, dryRun bool) string {
	verb := "migrated"
	if dryRun {
		verb = "would migrate"
	}
	var note strings.Builder
	for i := 0; i < len(proposals) && i < config.MaxExceptions; i++ {
		entry := proposals[i].Entry
		fmt.Fprintf(&note, "; %s exception %s %s: category %s, entered %s, expires %s (%s)",
			verb, entry.Rule, entry.Target(), entry.Category, entry.Entered, entry.Expires, proposals[i].Source)
	}
	return note.String()
}
