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

func TestCountSingleSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		b    *Baseline
		want int
	}{
		{"nil", nil, 0}, {"empty", &Baseline{}, 0}, {"absent", &Baseline{Absent: true}, 0},
		{"understated", &Baseline{Infractions: []Infraction{{RuleID: "HISS-01"}}}, 1},
		{"unitemized", &Baseline{TotalInfractions: 7}, 7}, {"negative", &Baseline{TotalInfractions: -1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.b.Count(); got != tc.want {
				t.Fatalf("Count=%d; want %d", got, tc.want)
			}
		})
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

func TestCheckMonotonicSingleTotal(t *testing.T) {
	previous := &Baseline{TotalInfractions: 1}
	for _, count := range []int{0, 1, 2} {
		err := CheckMonotonic(previous, &Baseline{TotalInfractions: count})
		if count <= 1 && err != nil {
			t.Fatal(err)
		}
		if count > 1 && (!errors.Is(err, ErrDebtIncrease) || !strings.Contains(err.Error(), "1 -> 2")) {
			t.Fatalf("growth=%v", err)
		}
	}
}
