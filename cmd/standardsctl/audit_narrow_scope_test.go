// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

func TestAuditNarrowScope(t *testing.T) {
	for _, tc := range []struct {
		name, body, failure string
	}{
		{"positive", "func F(b bool) { x := 1; if b { println(x) } }", "HISS-05"},
		{"negative", "func F(b bool) { x := 1; if b { println(x) }; println(x) }", ""},
		{"empty boundary", "func F() {}", ""},
		{"call initializer", "func compute() int { return 1 }; func F(b bool) { x := compute(); if b { println(x) } }", ""},
		{"error idiom", "func f() error { return nil }; func F() { err := f(); if err != nil { println(err) } }", ""},
		{"parse error", "func F( {", "hiss:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFixture(t)
			writeFixtureFile(t, f.dir, "scope.go", "package p\n"+tc.body+"\n")
			_, err := f.audit(t, "--offline")
			if tc.failure == "" {
				if err != nil {
					t.Fatalf("clean scope rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("audit error = %v, want %q", err, tc.failure)
			}
			if tc.name == "parse error" {
				assertUnparsedBaselineRefused(t, f)
			}
		})
	}
}

func assertUnparsedBaselineRefused(t *testing.T, f *auditFixture) {
	t.Helper()
	before := readFixtureFile(t, f.dir, ".standards-baseline.json")
	err := runBaseline([]string{"--record", "--file", f.baselinePath})
	if err == nil || !strings.Contains(err.Error(), "scan did not cover its scope") {
		t.Fatalf("baseline error = %v; unparsed source must be refused", err)
	}
	if after := readFixtureFile(t, f.dir, ".standards-baseline.json"); before != after {
		t.Fatal("incomplete scan changed the baseline")
	}
}
