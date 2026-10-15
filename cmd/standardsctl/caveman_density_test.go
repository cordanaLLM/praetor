// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
)

// #1087: density and limit print with enough precision to show the verdict. Negative: 2.04
// against the 2.0 limit no longer reads as equal. Boundary: a density equal to the limit
// prints equal, and one just above it (2.004) prints with the digit that separates them.
func TestFormatCavemanReport_Boundary_DensityShowsTheVerdict(t *testing.T) {
	for _, tc := range []struct {
		articles, words int
		want            string
	}{
		{51, 2500, "density=2.04/100 limit=2.00"},
		{2, 100, "density=2.00/100 limit=2.00"},
		{501, 25000, "density=2.004/100 limit=2.000"},
		{0, 0, "density=0.00/100 limit=2.00"},
	} {
		var out strings.Builder
		formatCavemanReport(&out, cavemanInput{name: "doc.md"}, caveman.Report{ProseWords: tc.words, Articles: tc.articles}, 0)
		if !strings.Contains(out.String(), " "+tc.want+" ") {
			t.Errorf("%d/%d: %s; want %q", tc.articles, tc.words, out.String(), tc.want)
		}
	}
}
