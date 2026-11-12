// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"fmt"
	"slices"
)

// MaxCompareIDs bounds each comparison input to 4096 IDs, leaving ample room
// above the 21-rule catalog while limiting caller-controlled work and storage.
const MaxCompareIDs = 4096

// CompareIDs reports canonical IDs missing from actual and actual IDs outside the
// canonical set. Each repeated actual ID is also invented. Both results are sorted.
// Inputs above MaxCompareIDs return an error naming the side and the bound.
func CompareIDs(canonical, actual []string) (missing, invented []string, err error) {
	if len(canonical) > MaxCompareIDs {
		return nil, nil, fmt.Errorf("canonical input has %d IDs, bound %d", len(canonical), MaxCompareIDs)
	}
	if len(actual) > MaxCompareIDs {
		return nil, nil, fmt.Errorf("actual input has %d IDs, bound %d", len(actual), MaxCompareIDs)
	}
	canonicalSet := compareIDSet(canonical)
	seen := make(map[string]bool, len(actual))
	for i := 0; i < len(actual) && i < MaxCompareIDs; i++ {
		id := actual[i]
		if !canonicalSet[id] || seen[id] {
			invented = append(invented, id)
		}
		seen[id] = true
	}
	for i := 0; i < len(canonical) && i < MaxCompareIDs; i++ {
		id := canonical[i]
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	slices.Sort(invented)
	return missing, invented, nil
}

// compareIDSet indexes at most MaxCompareIDs IDs for bounded membership lookups.
// CompareIDs validates input lengths before constructing the set.
func compareIDSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for i := 0; i < len(ids) && i < MaxCompareIDs; i++ {
		set[ids[i]] = true
	}
	return set
}
