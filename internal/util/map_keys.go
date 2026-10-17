// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"cmp"
	"maps"
	"slices"
)

// SortedKeys returns the keys of m in ascending order. If m is nil or empty,
// an empty (non-nil) slice is returned.
func SortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	if len(m) == 0 {
		return []K{}
	}
	return slices.Sorted(maps.Keys(m))
}
