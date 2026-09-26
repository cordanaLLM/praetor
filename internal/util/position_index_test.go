// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "testing"

func TestPositionIndex_Positive_MapsEachKeyToItsPosition(t *testing.T) {
	got := PositionIndex([]string{"a", "b", "c"})
	want := map[string]int{"a": 0, "b": 1, "c": 2}
	if len(got) != len(want) {
		t.Fatalf("PositionIndex returned %d keys, want %d: %v", len(got), len(want), got)
	}
	for key, pos := range want {
		if got[key] != pos {
			t.Fatalf("PositionIndex[%q] = %d, want %d", key, got[key], pos)
		}
	}
}

func TestPositionIndex_Negative_AbsentKeyIsNotPresent(t *testing.T) {
	got := PositionIndex([]string{"a"})
	if _, ok := got["b"]; ok {
		t.Fatalf("PositionIndex reported a position for a key it was never given: %v", got)
	}
}

func TestPositionIndex_Boundary_EmptyAndDuplicateKeys(t *testing.T) {
	if got := PositionIndex(nil); len(got) != 0 {
		t.Fatalf("PositionIndex(nil) = %v, want an empty map", got)
	}
	got := PositionIndex([]string{"x", "y", "x"})
	if got["x"] != 2 || got["y"] != 1 || len(got) != 2 {
		t.Fatalf("PositionIndex with a duplicate = %v, want x at its last position 2 and y at 1", got)
	}
}
