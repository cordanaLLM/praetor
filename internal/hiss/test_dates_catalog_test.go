// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss_test

import (
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"strings"
	"testing"
)

// TestFixtureDateCatalogExplainsClockIntegrity pins fixture date catalog explains clock integrity.
func TestFixtureDateCatalogExplainsClockIntegrity(t *testing.T) {
	rule, ok := hisscatalog.LookupRule("HISS-15")
	if !ok || !strings.Contains(rule.Specification, "test clock") || !strings.Contains(rule.Enforcement, "fixture") {
		t.Fatalf("clock finding has no catalog requirement: %+v", rule)
	}
	for _, term := range []string{"Positive", "Negative", "Boundary"} {
		if !strings.Contains(rule.Specification, term) {
			t.Errorf("lost dimension %s", term)
		}
	}
}
