// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clientid

import (
	"slices"
	"strings"
	"testing"
)

func TestKnownIsSortedUniqueAndACopy(t *testing.T) {
	ids := Known()
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Fatalf("known clients must be sorted and unique: %v", ids)
	}
	ids[0] = "changed"
	if Known()[0] != AGY {
		t.Fatal("Known returned the package slice, not a copy")
	}
}

func TestParseKnownClient(t *testing.T) {
	for _, id := range Known() {
		got, err := Parse(string(id))
		if err != nil || got != id {
			t.Fatalf("Parse(%q) = %q, %v", id, got, err)
		}
	}
}

func TestParseUnknownClientNamesTheSupportedSet(t *testing.T) {
	for _, value := range []string{"", "AGY", "agy ", "opencode", "cursor"} {
		_, err := Parse(value)
		if err == nil || !strings.Contains(err.Error(), "supported: agy, claude") {
			t.Fatalf("Parse(%q) error %v does not name the supported set", value, err)
		}
	}
}
