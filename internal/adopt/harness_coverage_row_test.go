// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscoverage"
)

// The harness is loaded into every session of every client, so a rule's row names the state per
// language and points at the catalog, and the mechanism prose stays in the catalog. Inlined, it
// took one repository's harness from 8 KB to 22 KB, past the cap of a client it declares.
//
// Positive: two claims render as their states and the catalog path. Negative: no mechanism text
// reaches the row. Boundary: an unknown rule, a rule without claims and no catalog stay empty, so
// the row falls through to the scanner.
func TestDeclaredEnforcementNamesStatesNotMechanisms(t *testing.T) {
	prose := strings.Repeat("a long mechanism sentence ", 200)
	catalog := &hisscoverage.Catalog{Rules: []hisscoverage.Rule{
		{ID: "HISS-01", Coverage: []hisscoverage.Coverage{
			{Language: "go", State: hisscoverage.StatePartial, Mechanism: prose},
			{Language: "c", State: hisscoverage.StateManual, Mechanism: prose},
		}},
		{ID: "HISS-02"},
	}}
	got := declaredEnforcement("HISS-01", catalog)
	if want := "partial (go), manual (c): " + hisscoverage.CatalogFile; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	for name, cell := range map[string]string{
		"unknown rule":      declaredEnforcement("HISS-03", catalog),
		"rule, no claims":   declaredEnforcement("HISS-02", catalog),
		"no catalog at all": declaredEnforcement("HISS-01", nil),
	} {
		if cell != "" {
			t.Errorf("%s: row = %q, want empty", name, cell)
		}
	}
}
