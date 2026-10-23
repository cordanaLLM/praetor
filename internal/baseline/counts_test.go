// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package baseline

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/readmerules"
)

func TestCountsAndSplitClasses(t *testing.T) {
	entries := []Infraction{{RuleID: "HISS-07"}, {RuleID: readmerules.RuleIDAbsent}, {RuleID: readmerules.RuleIDTagline}}
	cases := []struct {
		name string
		base *Baseline
		want ClassCounts
	}{
		{"nil", nil, ClassCounts{}},
		{"empty", &Baseline{}, ClassCounts{}},
		{"absent", &Baseline{Absent: true}, ClassCounts{}},
		{"mixed", &Baseline{Infractions: entries, TotalInfractions: 3}, ClassCounts{HISS: 1, README: 2}},
		{"understated", &Baseline{Infractions: entries}, ClassCounts{README: 2}},
		{"below README count", &Baseline{Infractions: entries, TotalInfractions: 1}, ClassCounts{README: 2}},
		{"README count only", &Baseline{Infractions: entries, TotalInfractions: 2}, ClassCounts{README: 2}},
		{"understated HISS only", &Baseline{Infractions: entries[:1]}, ClassCounts{}},
		{"legacy total", &Baseline{Infractions: entries, TotalInfractions: 5}, ClassCounts{HISS: 3, README: 2}},
		{"total only", &Baseline{TotalInfractions: 7}, ClassCounts{HISS: 7}},
		{"negative total", &Baseline{TotalInfractions: -1}, ClassCounts{}},
		{"unknown rule", &Baseline{Infractions: []Infraction{{RuleID: "README-99"}}, TotalInfractions: 1}, ClassCounts{HISS: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.base.Counts(); got != tc.want || tc.base.Count() != tc.want.Total() {
				t.Fatalf("Counts() = %+v; want %+v", got, tc.want)
			}
			hiss, readme := tc.base.SplitClasses()
			if hiss.Counts().HISS != tc.want.HISS || readme.Counts().README != tc.want.README {
				t.Fatalf("split counts: HISS=%+v README=%+v", hiss, readme)
			}
		})
	}
}

func TestSplitClassesPreservesMetadataAndEntries(t *testing.T) {
	b := &Baseline{Version: 2, Repository: "fixture/repo", CommitSHA: "abc", IncreaseRationale: "legacy",
		Infractions: []Infraction{{RuleID: "HISS-07"}, {RuleID: readmerules.RuleIDAbsent}}}
	hiss, readme := b.SplitClasses()
	if hiss.Repository != b.Repository || readme.CommitSHA != b.CommitSHA || hiss.Version != b.Version || readme.IncreaseRationale != b.IncreaseRationale {
		t.Fatal("split discarded metadata")
	}
	hiss.Infractions[0].RuleID = "changed"
	readme.Infractions[0].RuleID = "changed"
	if b.Infractions[0].RuleID != "HISS-07" || b.Infractions[1].RuleID != readmerules.RuleIDAbsent {
		t.Fatal("split aliases original entries")
	}
}

func TestIsReadmeRule(t *testing.T) {
	for _, rule := range []string{readmerules.RuleIDAbsent, readmerules.RuleIDBadgeBlock, readmerules.RuleIDTagline,
		readmerules.RuleIDHero, readmerules.RuleIDDiagrams, readmerules.RuleIDSections} {
		if !IsReadmeRule(rule) {
			t.Errorf("README rule rejected: %s", rule)
		}
	}
	for _, rule := range []string{"", "README-0", "README-06", "README-000", "readme-00", "HISS-07"} {
		if IsReadmeRule(rule) {
			t.Errorf("non-README rule accepted: %s", rule)
		}
	}
}

func TestCheckMonotonicNamesGrowingClass(t *testing.T) {
	previous := &Baseline{TotalInfractions: 2, Infractions: []Infraction{{RuleID: "HISS-07"}, {RuleID: readmerules.RuleIDAbsent}}}
	for _, rule := range []string{"HISS-07", readmerules.RuleIDAbsent} {
		next := &Baseline{TotalInfractions: 2, Infractions: []Infraction{{RuleID: rule}, {RuleID: rule}}}
		err := CheckMonotonic(previous, next)
		class := "HISS"
		if IsReadmeRule(rule) {
			class = "README"
		}
		if !errors.Is(err, ErrDebtIncrease) || !strings.Contains(err.Error(), class+" increased: 1 -> 2") {
			t.Fatalf("growing %s class: %v", class, err)
		}
	}
}
