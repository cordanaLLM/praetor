// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"slices"
	"testing"
)

func TestSortedKeys_Positive(t *testing.T) {
	input := map[string]int{
		"zebra":  1,
		"apple":  2,
		"mango":  3,
		"banana": 4,
	}
	want := []string{"apple", "banana", "mango", "zebra"}
	got := SortedKeys(input)
	if !slices.Equal(got, want) {
		t.Fatalf("SortedKeys = %v, want %v", got, want)
	}
}

func TestSortedKeys_Negative(t *testing.T) {
	input := map[string]string{
		"b": "val2",
		"a": "val1",
	}
	got := SortedKeys(input)
	if slices.Equal(got, []string{"b", "a"}) {
		t.Fatal("SortedKeys must not return unsorted order")
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestSortedKeys_Boundary(t *testing.T) {
	var nilMap map[string]int
	if got := SortedKeys(nilMap); got == nil || len(got) != 0 {
		t.Fatalf("SortedKeys(nil) must return empty non-nil slice, got %#v", got)
	}
	emptyMap := map[string]int{}
	if got := SortedKeys(emptyMap); got == nil || len(got) != 0 {
		t.Fatalf("SortedKeys(empty) must return empty non-nil slice, got %#v", got)
	}
	single := map[int]string{42: "answer"}
	if got := SortedKeys(single); !slices.Equal(got, []int{42}) {
		t.Fatalf("SortedKeys(single) = %v, want [42]", got)
	}
}
