// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestHarnessCatalogDrift checks exact catalog membership, duplicates, and order independence.
func TestHarnessCatalogDrift(t *testing.T) {
	harness := renderTruthHarness(t, "", "fixture", truthPlan)
	rows, err := hisscatalog.InvariantTableRows(harness)
	if err != nil {
		t.Fatal(err)
	}
	ids := hisscatalog.RuleIDs()
	t.Run("current", func(t *testing.T) {
		if err := compareHarnessCatalog(rows, ids); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		added := append(slices.Clone(ids), "HISS-999")
		if err := compareHarnessCatalog(rows, added); err == nil || !strings.Contains(err.Error(), "missing HISS-999") {
			t.Fatalf("expected missing HISS-999, got %v", err)
		}
	})
	t.Run("extra", func(t *testing.T) {
		extraTable := strings.Replace(harness, hisscatalog.GatedInvariantsHeading, hisscatalog.GatedInvariantsHeading+"\n\n| **ACME-999** extra | rule | check | fail |", 1)
		extra, err := hisscatalog.InvariantTableRows(extraTable)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareHarnessCatalog(extra, ids); err == nil || !strings.Contains(err.Error(), "extra ACME-999") {
			t.Fatalf("expected extra ACME-999, got %v", err)
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		duplicate := append(slices.Clone(rows), rows[0])
		if err := compareHarnessCatalog(duplicate, ids); err == nil || !strings.Contains(err.Error(), "duplicate "+rows[0].ID) {
			t.Fatalf("expected duplicate %s, got %v", rows[0].ID, err)
		}
	})
	t.Run("order", func(t *testing.T) {
		reversed := slices.Clone(rows)
		slices.Reverse(reversed)
		if err := compareHarnessCatalog(reversed, ids); err != nil {
			t.Fatal(err)
		}
	})
}

// compareHarnessCatalog compares the IDs of the rendered rows with the expected catalog.
func compareHarnessCatalog(rows []hisscatalog.InvariantRow, ids []string) error {
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.ID]++
	}
	var problems []string
	for _, id := range ids {
		if counts[id] == 0 {
			problems = append(problems, "missing "+id)
		}
		delete(counts, id)
	}
	for _, row := range rows {
		if counts[row.ID] > 0 {
			problems = append(problems, "extra "+row.ID)
			delete(counts, row.ID)
		}
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.ID] {
			problems = append(problems, "duplicate "+row.ID)
		}
		seen[row.ID] = true
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("invariant table: %s", strings.Join(problems, ", "))
}
