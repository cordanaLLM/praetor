// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

// PositionIndex maps each key to its position in keys. A key that occurs more
// than once maps to its last position, so callers that need first-wins
// semantics must deduplicate first. A nil or empty slice yields an empty map.
func PositionIndex(keys []string) map[string]int {
	index := make(map[string]int, len(keys))
	for i, key := range keys {
		index[key] = i
	}
	return index
}
