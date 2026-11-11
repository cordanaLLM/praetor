// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"slices"
	"testing"
)

func TestCompareIDs(t *testing.T) {
	cases := []struct {
		name                                 string
		canonical, actual, missing, invented []string
	}{
		{name: "positive equal unordered", canonical: []string{"B", "A"}, actual: []string{"A", "B"}},
		{name: "negative missing and invented sorted", canonical: []string{"D", "B", "A"}, actual: []string{"Z", "A", "Y"}, missing: []string{"B", "D"}, invented: []string{"Y", "Z"}},
		{name: "boundary empty"},
		{name: "boundary missing all", canonical: []string{"A"}, missing: []string{"A"}},
		{name: "boundary invented all", actual: []string{"A"}, invented: []string{"A"}},
		{name: "boundary duplicates", canonical: []string{"A"}, actual: []string{"A", "A", "A", "X", "X"}, invented: []string{"A", "A", "X", "X"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			missing, invented := CompareIDs(tc.canonical, tc.actual)
			if !slices.Equal(missing, tc.missing) || !slices.Equal(invented, tc.invented) {
				t.Errorf("got missing %v, invented %v; want missing %v, invented %v", missing, invented, tc.missing, tc.invented)
			}
		})
	}
}
