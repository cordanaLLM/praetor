// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "sort"

// ChangedEntries lists, sorted, the keys whose value differs between before and after: a key
// whose value changed, one only after holds and one only before holds. It compares two
// snapshots of path to digest, such as a tree before and after a command wrote into it; the
// dogfood public snapshot and the generated-artefact renderings both read their changes here
// (HISS-19).
func ChangedEntries(before, after map[string]string) []string {
	changed := make([]string, 0)
	for key, value := range after {
		if held, ok := before[key]; !ok || held != value {
			changed = append(changed, key)
		}
	}
	for key := range before {
		if _, ok := after[key]; !ok {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}
