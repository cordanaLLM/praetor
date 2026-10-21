// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestRacePackagesParsing(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      []string
	}{
		{"none", "none", []string{}}, {"full root module", "./...", []string{"./..."}},
		{"exact packages", "example.test/a,example.test/b", []string{"example.test/a", "example.test/b"}},
		{"versioned import", "gopkg.in/yaml.v3", []string{"gopkg.in/yaml.v3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRacePackages(tc.raw)
			if err != nil || got == nil || !slices.Equal(got, tc.want) || FormatRacePackages(got) != tc.raw {
				t.Fatalf("roundtrip %q: got=%v err=%v", tc.raw, got, err)
			}
		})
	}
	for _, raw := range []string{"", " ", "a,", ",a", "a,,b", "a,a", "none,a", "./...,a", "./a", "../a", "/a",
		"-args", "a\nb", "a\tb", "a\\b", "a/...", "a//b", "a/..", "a/.", "a@v1", strings.Repeat("a", 1<<20+1)} {
		t.Run(fmt.Sprintf("invalid %.30q", raw), func(t *testing.T) {
			if _, err := ParseRacePackages(raw); err == nil {
				t.Fatalf("unsafe or ambiguous scope accepted: %.30q", raw)
			}
		})
	}
}

func TestRacePackagesCountBoundary(t *testing.T) {
	packages := make([]string, MaxRacePackages)
	for i := 0; i < len(packages) && i < MaxRacePackages; i++ {
		packages[i] = fmt.Sprintf("example.test/p%d", i)
	}
	if got, err := ParseRacePackages(strings.Join(packages, ",")); err != nil || len(got) != MaxRacePackages {
		t.Fatalf("inclusive count boundary rejected: count=%d err=%v", len(got), err)
	}
	if _, err := ParseRacePackages(strings.Join(append(packages, "example.test/extra"), ",")); err == nil {
		t.Fatal("scope beyond package bound accepted")
	}
}
