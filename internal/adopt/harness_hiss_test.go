// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// TestPlanLanguagesFollowsDetectedRuntimes: Positive: Go, Cargo, Python and native builds map
// to their languages, several at once. Negative: a runtime no clause names (Node, .NET) is the
// known "other" set, never Go. Boundary: no runtime, or no plan, is the unknown set.
func TestPlanLanguagesFollowsDetectedRuntimes(t *testing.T) {
	cases := []struct {
		runtimes []string
		want     hisscatalog.Language
	}{
		{[]string{"go", "cargo"}, hisscatalog.LanguageGo | hisscatalog.LanguageRust},
		{[]string{"python", "meson.build"}, hisscatalog.LanguagePython | hisscatalog.LanguageC},
		{[]string{"CMakeLists.txt"}, hisscatalog.LanguageC},
		{[]string{"node", "dotnet"}, hisscatalog.LanguageOther},
		{[]string{}, 0},
	}
	for _, tc := range cases {
		if got := planLanguages(&VerificationPlan{Runtimes: tc.runtimes}); got != tc.want {
			t.Errorf("planLanguages(%q) = %b, want %b", tc.runtimes, got, tc.want)
		}
	}
	if got := planLanguages(nil); got != 0 {
		t.Errorf("planLanguages(nil) = %b", got)
	}
}

// TestHarnessComplexityRowStatesTheEnforcedLimit: the HISS-04 row names the function length the
// repository's audit enforces once the policy is resolved (a stricter policy included), and the
// audit ceiling with its note before that (#68).
func TestHarnessComplexityRowStatesTheEnforcedLimit(t *testing.T) {
	s := &adoptSession{verification: truthPlan}
	if facts := s.hissFacts(); facts.MaxFuncLOC != 0 || facts.Languages != hisscatalog.LanguageGo {
		t.Fatalf("unresolved facts = %+v", facts)
	}
	s.policy = &config.EffectivePolicy{Policy: config.ResolvedPolicy{Complexity: config.ComplexityPolicy{MaxFuncLOC: 50}}}
	facts := adoptedFacts("", "fixture", "framework", truthPlan)
	facts.hiss = s.hissFacts()
	harness, err := buildAgentHarness(facts)
	if err != nil {
		t.Fatal(err)
	}
	row := invariantRows(harness)[3]
	if !strings.HasPrefix(row, "| **HISS-04** ") || !strings.Contains(row, "; func LOC <= 50 |") {
		t.Errorf("HISS-04 row does not state the enforced limit: %s", row)
	}
	unresolved := adoptedFacts("", "fixture", "framework", truthPlan)
	harness, err = buildAgentHarness(unresolved)
	if err != nil {
		t.Fatal(err)
	}
	if row := invariantRows(harness)[3]; !strings.Contains(row, "func LOC <= 60 (audit ceiling; stricter repository policy wins)") {
		t.Errorf("unresolved HISS-04 row: %s", row)
	}
}
