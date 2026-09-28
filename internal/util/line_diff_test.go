// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
)

// One changed line shows as a removal and an addition between the lines kept around it.
func TestUnifiedDiff_Positive_ChangedLineBetweenContext(t *testing.T) {
	before := "{\n  \"count\": 1,\n  \"name\": \"main\"\n}\n"
	after := "{\n  \"count\": 2,\n  \"name\": \"main\"\n}\n"
	want := "--- a/rules.json\n+++ b/rules.json\n@@ -1,4 +1,4 @@\n" +
		" {\n-  \"count\": 1,\n+  \"count\": 2,\n   \"name\": \"main\"\n }\n"
	if got := UnifiedDiff("rules.json", []byte(before), []byte(after)); got != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", got, want)
	}
}

// Identical input has no diff, and a change to the line terminators alone is still one.
func TestUnifiedDiff_Negative_IdenticalIsEmptyAndTerminatorsCount(t *testing.T) {
	if got := UnifiedDiff("a", []byte("x\ny\n"), []byte("x\ny\n")); got != "" {
		t.Fatalf("identical input rendered a diff:\n%s", got)
	}
	got := UnifiedDiff("a", []byte("x\r\ny\n"), []byte("x\ny\n"))
	if !strings.Contains(got, "-x\r\n+x\n y\n") {
		t.Fatalf("a CRLF line must show as changed:\n%q", got)
	}
}

// An empty side, a missing final newline and a side above MaxDiffLines are the edges.
func TestUnifiedDiff_Boundary_EmptySideNoFinalNewlineAndBound(t *testing.T) {
	created := UnifiedDiff("new.json", nil, []byte("{\n}"))
	want := "--- a/new.json\n+++ b/new.json\n@@ -0,0 +1,2 @@\n+{\n+}\n" + noNewlineMarker
	if created != want {
		t.Fatalf("creation diff:\n%q\nwant:\n%q", created, want)
	}
	if got := UnifiedDiff("f", []byte("x\n"), []byte("x")); !strings.Contains(got, "-x\n+x\n"+noNewlineMarker) {
		t.Fatalf("a dropped final newline must show:\n%q", got)
	}
	large := strings.Repeat("same\n", MaxDiffLines+1)
	got := UnifiedDiff("big", []byte(large), []byte(large+"tail\n"))
	if strings.Count(got, "\n-same\n") == 0 || strings.Count(got, "\n+same\n") == 0 || strings.Contains(got, "\n same\n") {
		t.Fatalf("a side above MaxDiffLines must be a whole replacement")
	}
	if strings.Count(got, "\n") != 3+2*(MaxDiffLines+1)+1 {
		t.Fatalf("replacement line count = %d", strings.Count(got, "\n"))
	}
}
