// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// TestExceptionsDispatchComplexity pins that functions in exceptions.go satisfy HISS-04 complexity bounds.
func TestExceptionsDispatchComplexity(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "exceptions.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	units := hiss.MeasureFile(fset, file)
	for i := 0; i < len(units); i++ {
		unit := units[i]
		if exceeded := unit.Metrics.Exceeded(hiss.ComplexityLimits{}); len(exceeded) != 0 {
			t.Errorf("%s exceeds HISS-04: %+v", unit.Name, exceeded)
		}
	}
}
