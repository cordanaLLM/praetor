// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package baseline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ReadmeMigration carries the planned snapshots, including on a read-only dry run.
type ReadmeMigration struct {
	HISS   *Baseline
	README *Baseline
	Moved  int
}

// MigrateReadme is adoption's only raw reader of legacy mixed baselines. It moves
// entries without recomputing fingerprints, timestamps or the legacy claimed debt.
func MigrateReadme(ctx context.Context, hissPath, readmePath string, dryRun bool) (*ReadmeMigration, error) {
	if ctx == nil {
		return nil, errors.New("baseline migration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	old, raw, err := loadBaselineRaw(hissPath)
	if err != nil {
		return nil, err
	}
	h, r := *old, *old
	h.Infractions = make([]Infraction, 0, len(old.Infractions))
	r.Infractions = make([]Infraction, 0)
	for i := 0; i < len(old.Infractions); i++ {
		if IsReadmeRule(old.Infractions[i].RuleID) {
			r.Infractions = append(r.Infractions, old.Infractions[i])
		} else {
			h.Infractions = append(h.Infractions, old.Infractions[i])
		}
	}
	result := &ReadmeMigration{HISS: old, Moved: len(r.Infractions)}
	if result.Moved == 0 {
		result.README, err = LoadClassBaseline(readmePath, READMEClass)
		return result, err
	}
	if _, err := os.Lstat(readmePath); !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("refusing migration from %s: %s already exists alongside %d README entries; reconcile both files before praetorctl adopt", hissPath, readmePath, result.Moved)
	}
	h.TotalInfractions = max(0, old.TotalInfractions-result.Moved)
	r.TotalInfractions = result.Moved
	result.HISS, result.README = &h, &r
	if dryRun {
		return result, nil
	}
	if err := migrateSnapshots(ctx, hissPath, readmePath, raw, result); err != nil {
		return nil, err
	}
	return result, nil
}

// migrateSnapshots refuses concurrent changes and rolls back only its own destination.
func migrateSnapshots(ctx context.Context, hissPath, readmePath string, raw []byte, m *ReadmeMigration) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	hissData, err := snapshotJSON(m.HISS)
	if err != nil {
		return err
	}
	readmeData, err := snapshotJSON(m.README)
	if err != nil {
		return err
	}
	if err := contextopt.ReplaceSnapshot(ctx, readmePath, readmeData, contextopt.ReplaceOptions{Mode: FilePerm}); err != nil {
		return err
	}
	if err := contextopt.ReplaceSnapshot(ctx, hissPath, hissData, contextopt.ReplaceOptions{Expected: raw, Exists: true, Mode: FilePerm}); err != nil {
		return errors.Join(err, contextopt.RemoveSnapshot(ctx, readmePath, readmeData))
	}
	return nil
}

// writeSnapshot is shared by normal saves and migration, which retains totals and dates.
func writeSnapshot(path string, b *Baseline) error {
	data, err := snapshotJSON(b)
	if err != nil {
		return err
	}
	if err := util.WriteFileAt(path, data, FilePerm); err != nil {
		return fmt.Errorf("failed to write baseline to %s: %w", path, err)
	}
	return nil
}

func snapshotJSON(b *Baseline) ([]byte, error) {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal baseline: %w", err)
	}
	return append(data, '\n'), nil
}
