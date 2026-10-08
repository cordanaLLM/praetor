// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitCSV_Positive(t *testing.T) {
	if got := SplitCSV(" go , test,docs "); !slices.Equal(got, []string{"go", "test", "docs"}) {
		t.Fatalf("SplitCSV = %q", got)
	}
}

func TestSplitCSV_Negative(t *testing.T) {
	for _, raw := range []string{"", "   ", "\t"} {
		if got := SplitCSV(raw); got != nil {
			t.Errorf("SplitCSV(%q) = %q, want nil", raw, got)
		}
	}
	if got := SplitCSV(" , ,"); len(got) != 0 {
		t.Errorf("blank fields must be dropped: %q", got)
	}
}

func TestSplitCSV_Boundary(t *testing.T) {
	if got := SplitCSV(strings.Repeat("a,", MaxCSVFields-1) + "a"); len(got) != MaxCSVFields {
		t.Fatalf("a list of %d fields must be read whole: %d", MaxCSVFields, len(got))
	}
	if got := SplitCSV(strings.Repeat("a,", MaxCSVFields) + "a"); len(got) != MaxCSVFields {
		t.Fatalf("a list past %d fields must stop at the bound: %d", MaxCSVFields, len(got))
	}
}
