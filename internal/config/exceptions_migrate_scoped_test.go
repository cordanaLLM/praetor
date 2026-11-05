// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Scoped migration repairs partial metadata and never resolves an unselected rule.
func TestExceptionMigrationForRulesPartialAndScope(t *testing.T) {
	today := ExceptionDay(time.Now())
	entered := today.AddDate(0, 0, -5).Format(ExceptionDateLayout)
	for _, partial := range []string{"category: D", "entered: " + entered} {
		path := migrationManifest(t, legacyMigrationEntry(20, "migration", today)+"    "+partial+"\n"+
			"  - rule: HISS-04\n    path: main.go\n    reason: hand-written\n    expires: "+today.AddDate(0, 0, 121).Format(ExceptionDateLayout)+"\n")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		data, proposals, err := PlanExceptionMigrationForRules(t.Context(), path, before, today, []string{ExceptionRuleDeclaredLinters})
		if err != nil || len(proposals) != 1 || proposals[0].Unresolved != "" {
			t.Fatalf("proposals=%+v err=%v", proposals, err)
		}
		manifest, err := DecodeManifest(data)
		if err != nil {
			t.Fatal(err)
		}
		assertExceptionMigrationFields(t, manifest.Exceptions, partial, entered)
		if err := ValidateExceptions(manifest.Exceptions, today); err == nil || !strings.Contains(err.Error(), "exceptions[1]") {
			t.Fatalf("hand-written entry no longer blocks audit: %v", err)
		}
	}
}

func assertExceptionMigrationFields(t *testing.T, entries []Exception, partial, entered string) {
	t.Helper()
	entry, other := entries[0], entries[1]
	if entry.Category == "" || entry.Entered == "" || other.Category != "" || other.Entered != "" {
		t.Fatalf("incorrect migration scope: %+v", entries)
	}
	if partial == "category: D" && entry.Category != "D" || partial != "category: D" && entry.Entered != entered {
		t.Fatalf("existing field replaced: %+v", entry)
	}
}

func TestExceptionMigrationForRulesEmptySelectionUnchanged(t *testing.T) {
	today := ExceptionDay(time.Now())
	path := migrationManifest(t, legacyMigrationEntry(20, "migration", today))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, proposals, err := PlanExceptionMigrationForRules(t.Context(), path, before, today, nil)
	if err != nil || len(proposals) != 0 || !bytes.Equal(data, before) {
		t.Fatalf("empty selection migrated input: proposals=%+v err=%v", proposals, err)
	}
}

func TestExceptionMigrationForRulesRefusesInvalidAndCancelled(t *testing.T) {
	for _, data := range []string{"version: 1\nversion: 1\n", "version: 1\nunknown: true\n"} {
		if _, _, err := PlanExceptionMigrationForRules(t.Context(), "unused", []byte(data), time.Now(), nil); err == nil {
			t.Fatalf("invalid input accepted: %q", data)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := PlanExceptionMigrationForRules(ctx, "unused", []byte("version: 1\n"), time.Now(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestExceptionMigrationForRulesUnresolvedPreservesEntry(t *testing.T) {
	today := ExceptionDay(time.Now())
	path := migrationManifest(t, legacyMigrationEntry(121, "migration", today))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data, proposals, err := PlanExceptionMigrationForRules(t.Context(), path, before, today, []string{ExceptionRuleDeclaredLinters})
	if err != nil || len(proposals) != 1 || proposals[0].Unresolved == "" {
		t.Fatalf("unresolvable expiry accepted: proposals=%+v err=%v", proposals, err)
	}
	manifest, err := DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if entry := manifest.Exceptions[0]; entry.Category != "" || entry.Entered != "" {
		t.Fatalf("unresolved entry edited: %+v", entry)
	}
}
