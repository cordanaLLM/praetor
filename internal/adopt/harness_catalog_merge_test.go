// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestHarnessCatalogMergedTable checks the final refresh merge: every catalog ID
// occurs exactly once and only repository-owned rows outside HISS are preserved.
func TestHarnessCatalogMergedTable(t *testing.T) {
	harness := renderTruthHarness(t, "", "fixture", truthPlan)
	const ownRow = "| **ACME-01** local rule | keep | review | blocker |"
	const staleRow = "| **HISS-999** stale rule | discard | none | ignored |"
	existing := strings.Replace(harness, hisscatalog.GatedInvariantsHeading,
		hisscatalog.GatedInvariantsHeading+"\n\n"+ownRow+"\n"+staleRow, 1)
	kept, ok, err := splitHarnessAdditions(existing, harness)
	if err != nil || !ok {
		t.Fatalf("split harness: found %v, error %v", ok, err)
	}
	merged, err := kept.join(harness)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := hisscatalog.InvariantTableRows(merged)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	assertCatalogIDs(t, hisscatalog.RuleIDs(), ids, nil, []string{"ACME-01"})
	if strings.Count(merged, ownRow) != 1 {
		t.Fatal("merged harness must preserve the repository-owned row exactly once, as written")
	}
}
