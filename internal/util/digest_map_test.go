// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

// ChangedEntries (#696): positive, a changed, an added and a removed key are listed sorted;
// negative, equal snapshots list nothing; boundary, nil and empty snapshots, and an empty value
// that appears, which is a change although a missing key reads as empty.
func TestChangedEntries_3D(t *testing.T) {
	before := map[string]string{"b": "1", "c": "2", "d": "3"}
	after := map[string]string{"a": "9", "b": "1", "c": "changed"}
	if got := strings.Join(ChangedEntries(before, after), ","); got != "a,c,d" {
		t.Fatalf("changed = %q, want a,c,d", got)
	}
	if got := ChangedEntries(before, map[string]string{"b": "1", "c": "2", "d": "3"}); len(got) != 0 {
		t.Fatalf("equal snapshots = %q", got)
	}
	if got := ChangedEntries(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("nil snapshots = %#v, want an empty non-nil list", got)
	}
	if got := strings.Join(ChangedEntries(nil, map[string]string{"x": ""}), ","); got != "x" {
		t.Fatalf("an appearing empty value = %q, want x", got)
	}
	if got := strings.Join(ChangedEntries(map[string]string{"x": ""}, map[string]string{}), ","); got != "x" {
		t.Fatalf("a disappearing empty value = %q, want x", got)
	}
}
