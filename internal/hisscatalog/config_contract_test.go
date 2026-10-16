// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog_test

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

var configContractFuncLength = regexp.MustCompile(`Function Length <= ([0-9]+) LOC`)

func TestRuleExplanation_Positive_HISS04StatesTheAuditFuncLOC(t *testing.T) {
	rule, _ := hisscatalog.LookupRule("HISS-04")
	want := strconv.Itoa(config.AuditMaxFuncLOC)
	got := configContractFuncLength.FindAllStringSubmatch(rule.Explanation(), -1)
	if len(got) != 1 || got[0][1] != want {
		t.Fatalf("HISS-04 function lengths = %v, want [%s]", got, want)
	}
}

func TestRuleExplanation_Boundary_HISS04ListKeepsItsShape(t *testing.T) {
	rule, _ := hisscatalog.LookupRule("HISS-04")
	want := "\n  - McCabe Cyclomatic Complexity <= 10\n  - Cognitive Complexity <= 15\n  - Function Length <= " +
		strconv.Itoa(config.AuditMaxFuncLOC) + " LOC\n  - Executable Statements <= 50"
	if rule.Specification != want {
		t.Fatalf("HISS-04 specification = %q, want %q", rule.Specification, want)
	}
}

func TestAdoptedDirective_Negative_ComplexityMatchesTheAuditCeiling(t *testing.T) {
	rule, _ := hisscatalog.LookupRule("HISS-04")
	ceiling := config.HISSComplexityCeiling()
	if ceiling.MaxFuncLOC != config.AuditMaxFuncLOC {
		t.Fatalf("audit ceiling %d, config.AuditMaxFuncLOC %d", ceiling.MaxFuncLOC, config.AuditMaxFuncLOC)
	}
	want := fmt.Sprintf("McCabe cyclomatic <= %d, cognitive <= %d, statements <= %d; func LOC <= %d (audit ceiling; stricter repository policy wins)",
		ceiling.MaxCyclomatic, ceiling.MaxCognitive, ceiling.MaxStatements, ceiling.MaxFuncLOC)
	if got := rule.AdoptedDirective(hisscatalog.Facts{CeilingFuncLOC: config.AuditMaxFuncLOC}); got != want {
		t.Fatalf("HISS-04 directive = %q, want %q", got, want)
	}
}
