// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestCompareIDs checks sorted drift, empty inputs, and repeated actual IDs.
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
			missing, invented, err := CompareIDs(tc.canonical, tc.actual)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(missing, tc.missing) || !slices.Equal(invented, tc.invented) {
				t.Errorf("got missing %v, invented %v; want missing %v, invented %v", missing, invented, tc.missing, tc.invented)
			}
		})
	}
}

// TestCompareIDsLimit accepts either input exactly at the bound without
// truncating results, including when both inputs reach the bound together.
func TestCompareIDsLimit(t *testing.T) {
	ids := make([]string, MaxCompareIDs)
	for i := 0; i < MaxCompareIDs; i++ {
		ids[i] = strconv.Itoa(i)
	}
	slices.Sort(ids)
	cases := []struct {
		name                                 string
		canonical, actual, missing, invented []string
	}{
		{name: "both at bound", canonical: ids, actual: ids},
		{name: "canonical at bound", canonical: ids, missing: ids},
		{name: "actual at bound", actual: ids, invented: ids},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			missing, invented, err := CompareIDs(tc.canonical, tc.actual)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(missing, tc.missing) || !slices.Equal(invented, tc.invented) {
				t.Fatalf("got missing %v, invented %v; want missing %v, invented %v", missing, invented, tc.missing, tc.invented)
			}
		})
	}
}

// TestCompareIDsOverLimit rejects either oversized input without partial results
// and requires an error naming the oversized side and the scalar bound.
func TestCompareIDsOverLimit(t *testing.T) {
	ids := make([]string, MaxCompareIDs+1)
	cases := []struct {
		side              string
		canonical, actual []string
	}{
		{side: "canonical", canonical: ids},
		{side: "actual", actual: ids},
	}
	for _, tc := range cases {
		t.Run(tc.side, func(t *testing.T) {
			missing, invented, err := CompareIDs(tc.canonical, tc.actual)
			if err == nil || !strings.Contains(err.Error(), tc.side) || !strings.Contains(err.Error(), strconv.Itoa(MaxCompareIDs)) {
				t.Fatalf("error = %v; want side %s and bound %d", err, tc.side, MaxCompareIDs)
			}
			if missing != nil || invented != nil {
				t.Fatalf("oversized input returned partial results: missing %v, invented %v", missing, invented)
			}
		})
	}
}
