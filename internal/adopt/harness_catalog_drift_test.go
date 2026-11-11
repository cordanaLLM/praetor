// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
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
	rowIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		rowIDs = append(rowIDs, row.ID)
	}
	t.Run("current", func(t *testing.T) {
		assertCatalogIDs(t, ids, rowIDs, nil, nil)
	})
	t.Run("missing", func(t *testing.T) {
		added := append(slices.Clone(ids), "HISS-999")
		assertCatalogIDs(t, added, rowIDs, []string{"HISS-999"}, nil)
	})
	t.Run("extra", func(t *testing.T) {
		extraTable := strings.Replace(harness, hisscatalog.GatedInvariantsHeading, hisscatalog.GatedInvariantsHeading+"\n\n| **ACME-999** extra | rule | check | fail |", 1)
		extra, err := hisscatalog.InvariantTableRows(extraTable)
		if err != nil {
			t.Fatal(err)
		}
		extraIDs := make([]string, 0, len(extra))
		for _, row := range extra {
			extraIDs = append(extraIDs, row.ID)
		}
		assertCatalogIDs(t, ids, extraIDs, nil, []string{"ACME-999"})
	})
	t.Run("duplicate", func(t *testing.T) {
		duplicate := append(slices.Clone(rowIDs), "HISS-01")
		assertCatalogIDs(t, ids, duplicate, nil, []string{"HISS-01"})
	})
	t.Run("order", func(t *testing.T) {
		reversed := slices.Clone(rowIDs)
		slices.Reverse(reversed)
		assertCatalogIDs(t, ids, reversed, nil, nil)
	})
}

// assertCatalogIDs fails the test unless hisscatalog.CompareIDs reports exactly the wanted
// missing and invented IDs for actual against canonical.
func assertCatalogIDs(t *testing.T, canonical, actual, wantMissing, wantInvented []string) {
	t.Helper()
	missing, invented := hisscatalog.CompareIDs(canonical, actual)
	if !slices.Equal(missing, wantMissing) || !slices.Equal(invented, wantInvented) {
		t.Fatalf("missing %v, invented %v; want missing %v, invented %v", missing, invented, wantMissing, wantInvented)
	}
}
