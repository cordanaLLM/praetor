// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package baseline

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateReadmeBoundary(t *testing.T) {
	for _, tc := range []migrationCase{
		{"empty", 0, 0, 0}, {"HISS only", 7, 3, 0}, {"README only below total", 0, 0, 2},
		{"floor", 1, 3, 2}, {"large", 1601, 1500, 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkMigrationBoundary(t, tc)
		})
	}
}

func TestMigrateReadmeMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	h, r := filepath.Join(dir, "h.json"), filepath.Join(dir, "r.json")
	m, err := MigrateReadme(t.Context(), h, r, false)
	if err != nil || !m.HISS.Absent || !m.README.Absent || m.Moved != 0 {
		t.Fatalf("missing=%+v %v", m, err)
	}
	if _, err := os.Stat(h); !os.IsNotExist(err) {
		t.Fatalf("missing migration wrote HISS: %v", err)
	}
	if err := os.WriteFile(h, []byte("{"), FilePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateReadme(t.Context(), h, r, true); err == nil {
		t.Fatal("accepted malformed legacy file")
	}
}

type migrationCase struct {
	name                string
	total, hiss, readme int
}

func checkMigrationBoundary(t *testing.T, tc migrationCase) {
	t.Helper()
	dir := t.TempDir()
	h, r := filepath.Join(dir, "h.json"), filepath.Join(dir, "r.json")
	b := &Baseline{Version: 1, TotalInfractions: tc.total, Infractions: make([]Infraction, 0, tc.hiss+tc.readme)}
	for i := 0; i < tc.hiss; i++ {
		b.Infractions = append(b.Infractions, Infraction{RuleID: "HISS-01"})
	}
	for i := 0; i < tc.readme; i++ {
		b.Infractions = append(b.Infractions, Infraction{RuleID: "README-01"})
	}
	if err := writeSnapshot(h, b); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(h)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := MigrateReadme(t.Context(), h, r, false)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Moved != tc.readme || len(migrated.HISS.Infractions) != tc.hiss {
		t.Fatalf("migration=%+v", migrated)
	}
	assertMigrationTotals(t, tc, migrated)
	if tc.readme == 0 {
		assertMigrationUnchanged(t, h, before)
	}
}

func assertMigrationTotals(t *testing.T, tc migrationCase, migrated *ReadmeMigration) {
	t.Helper()
	if tc.readme == 0 {
		return
	}
	if migrated.HISS.TotalInfractions != max(0, tc.total-tc.readme) || migrated.README.TotalInfractions != tc.readme {
		t.Fatal("migration changed claimed totals")
	}
}

func assertMigrationUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("no-op rewrote HISS: %v", err)
	}
}

func TestMigrationSnapshotRefusesStaleSourceAndExistingDestination(t *testing.T) {
	dir := t.TempDir()
	h, r := filepath.Join(dir, "h.json"), filepath.Join(dir, "r.json")
	source := []byte(`{"infractions":[{"rule_id":"HISS-01"}]}`)
	if err := os.WriteFile(h, source, FilePerm); err != nil {
		t.Fatal(err)
	}
	m := &ReadmeMigration{HISS: &Baseline{Version: 1}, README: &Baseline{Version: 1, TotalInfractions: 1, Infractions: []Infraction{{RuleID: "README-01"}}}}
	if err := migrateSnapshots(t.Context(), h, r, []byte("stale"), m); err == nil {
		t.Fatal("migration overwrote changed source")
	}
	if _, err := os.Stat(r); !os.IsNotExist(err) {
		t.Fatalf("failed migration left destination: %v", err)
	}
	assertMigrationUnchanged(t, h, source)
	if err := os.WriteFile(r, source, FilePerm); err != nil {
		t.Fatal(err)
	}
	if err := migrateSnapshots(t.Context(), h, r, source, m); err == nil {
		t.Fatal("migration overwrote existing destination")
	}
	assertMigrationUnchanged(t, h, source)
	assertMigrationUnchanged(t, r, source)
}

func TestMigrateReadmeRefusesCorruptDestinationConflict(t *testing.T) {
	dir := t.TempDir()
	h, r := filepath.Join(dir, "h.json"), filepath.Join(dir, "r.json")
	if err := writeSnapshot(h, &Baseline{Infractions: []Infraction{{RuleID: "README-01"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r, []byte("{"), FilePerm); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateReadme(t.Context(), h, r, true); err == nil || !strings.Contains(err.Error(), h) || !strings.Contains(err.Error(), r) {
		t.Fatalf("conflict=%v", err)
	}
}

func TestMigrateReadmeCancellationLeavesFilesUntouched(t *testing.T) {
	dir := t.TempDir()
	h, r := filepath.Join(dir, "h.json"), filepath.Join(dir, "r.json")
	if err := writeSnapshot(h, &Baseline{Infractions: []Infraction{{RuleID: "README-01"}}}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(h)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := MigrateReadme(ctx, h, r, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	assertMigrationUnchanged(t, h, before)
	if _, err := os.Stat(r); !os.IsNotExist(err) {
		t.Fatalf("cancelled migration wrote README baseline: %v", err)
	}
	var nilContext context.Context
	if _, err := MigrateReadme(nilContext, h, r, false); err == nil {
		t.Fatal("accepted nil context")
	}
}
