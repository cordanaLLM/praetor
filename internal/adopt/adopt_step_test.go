// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"testing"
)

// adoptOrStep runs a whole adoption with opts when whole is set, and otherwise only step on the
// session adoption prepares for opts (prepareAdoption), returning the report either way. A test
// that replays one step over many prior-text fixtures keeps one whole adoption and runs the rest
// through the step: a whole adoption costs seconds on Windows, and the internal/adopt package ran
// into its 30-minute timeout there (office-w19a-c1-adopt).
func adoptOrStep(t *testing.T, opts AdoptOptions, whole bool, step func(context.Context, *adoptSession) error) (*AdoptReport, error) {
	t.Helper()
	if whole {
		return Adopt(t.Context(), opts)
	}
	report, s, err := prepareAdoption(t.Context(), opts)
	if err != nil {
		return report, err
	}
	return report, step(t.Context(), s)
}

// stepFixture writes files below a fresh test repository named name and runs adoptOrStep over it
// with step, failing on an error or a report error.
func stepFixture(t *testing.T, name string, files map[string]string, whole bool, step func(context.Context, *adoptSession) error) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	for rel, body := range files {
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(rel)), body)
	}
	rep, err := adoptOrStep(t, AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}, whole, step)
	if err != nil {
		t.Fatalf("adopt %s: %v", name, err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// makefileAndEditors is the makefile step and the editors step after it, which offers the
// verify-all rule the makefile step leaves (adoptSteps).
func makefileAndEditors(ctx context.Context, s *adoptSession) error {
	if err := reconcileMakefile(ctx, s); err != nil {
		return err
	}
	return reconcileEditors(ctx, s)
}
