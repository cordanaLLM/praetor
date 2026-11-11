// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import "slices"

// CompareIDs reports canonical IDs missing from actual and actual IDs outside the
// canonical set. Each repeated actual ID is also invented. Both results are sorted.
func CompareIDs(canonical, actual []string) (missing, invented []string) {
	for _, id := range canonical {
		if !slices.Contains(actual, id) {
			missing = append(missing, id)
		}
	}
	seen := make(map[string]bool, len(actual))
	for _, id := range actual {
		if !slices.Contains(canonical, id) || seen[id] {
			invented = append(invented, id)
		}
		seen[id] = true
	}
	slices.Sort(missing)
	slices.Sort(invented)
	return missing, invented
}
