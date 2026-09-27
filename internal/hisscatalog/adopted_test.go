// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
)

// TestAdoptedFields_Positive_EveryRuleStatesItsRow: a rule missing its scope or directive
// would render an empty cell in every adopted AGENTS.md, and an adopted check without stages,
// or a blocking one without a trigger, would claim a gate that blocks nothing.
func TestAdoptedFields_Positive_EveryRuleStatesItsRow(t *testing.T) {
	for _, rule := range Rules() {
		if strings.TrimSpace(rule.Scope) == "" || len(rule.Directive) == 0 {
			t.Errorf("%s has no scope or directive: %+v", rule.ID, rule)
		}
		for _, clause := range rule.Directive {
			if strings.TrimSpace(clause.Text) == "" {
				t.Errorf("%s has an empty directive clause", rule.ID)
			}
		}
		adoption := rule.Adoption
		if (adoption.Check == "") != (adoption.Stages == 0) {
			t.Errorf("%s: adopted check %q and stages %b must be set together", rule.ID, adoption.Check, adoption.Stages)
		}
		if check, failure := rule.Adopted(AllPipelines); check != NotEnforced && failure != recordsOnly && adoption.Trigger == "" {
			t.Errorf("%s: blocking check %q names no trigger", rule.ID, check)
		}
	}
}

// TestAdoptedFields_Positive_PassCavemanLint: the directives and adopted cells land in every
// adopted AGENTS.md, which the context gate lints, so together they must lint clean.
func TestAdoptedFields_Positive_PassCavemanLint(t *testing.T) {
	var text strings.Builder
	for _, rule := range Rules() {
		for _, generated := range []Pipeline{AllPipelines, PipelineVerifyAll, PipelineLefthook, 0} {
			check, failure := rule.Adopted(generated)
			text.WriteString(rule.Scope + ": " + rule.AdoptedDirective(Facts{}) + ". " + check + ". " + failure + ".\n")
		}
	}
	if report := caveman.Check(text.String(), caveman.Options{}); !report.Passed() || report.ProseWords == 0 {
		t.Fatalf("catalog harness text must lint clean: %+v", report.Findings)
	}
}

// TestReference_Boundary_TitleAndExplanationAgree: Reference is the prefix Explanation
// opens with, so prose citing a rule and the tool's answer name it the same way.
func TestReference_Boundary_TitleAndExplanationAgree(t *testing.T) {
	rule, _ := LookupRule("HISS-14")
	if rule.Reference() != "HISS-14 (Append-Only ABI & Migration Footers)" {
		t.Fatalf("HISS-14 reference = %q", rule.Reference())
	}
	if !strings.HasPrefix(rule.Explanation(), "Rule: "+rule.Reference()+"\n") {
		t.Fatalf("explanation does not open with the reference: %q", rule.Explanation())
	}
}

// TestAdoptedExplanation_Positive_StatesAdoptedEnforcement: the served explanation is the
// rule's own explanation followed by what an adopted repository enforces, and a rule adoption
// does not check says so instead of repeating praetor's own mechanism (BUG-804).
func TestAdoptedExplanation_Positive_StatesAdoptedEnforcement(t *testing.T) {
	audit, _ := LookupRule("HISS-01")
	text := audit.AdoptedExplanation()
	if !strings.HasPrefix(text, audit.Explanation()+"\nAdopted repositories: 'praetorctl audit' HISS scan in verify-all + lefthook pre-commit/pre-push") ||
		!strings.Contains(text, "Holds only where adoption generated that pipeline") {
		t.Fatalf("HISS-01 adopted explanation:\n%s", text)
	}
	unchecked, _ := LookupRule("HISS-12")
	if text := unchecked.AdoptedExplanation(); text != unchecked.Explanation()+"\nAdopted repositories: not enforced; adoption generates no check for this rule." {
		t.Fatalf("HISS-12 adopted explanation:\n%s", text)
	}
	if check, failure := unchecked.Adopted(AllPipelines); check != NotEnforced || failure != Advisory {
		t.Fatalf("HISS-12 Adopted() = %q, %q", check, failure)
	}
}

// TestAdopted_Boundary_FollowsGeneratedPipelines: a check is credited only to the pipelines
// adoption generated. Positive: both pipelines name every stage. Negative: neither names
// none. Boundary: one pipeline keeps exactly its own stages and consequences, and a check
// that only records blocks nothing.
func TestAdopted_Boundary_FollowsGeneratedPipelines(t *testing.T) {
	drift, _ := LookupRule("HISS-16")
	cases := []struct {
		generated      Pipeline
		check, failure string
	}{
		{AllPipelines, "`praetorctl compile-context --verify` in verify-all + lefthook pre-commit", "drift fails verify-all, blocks commit"},
		{PipelineVerifyAll, "`praetorctl compile-context --verify` in verify-all", "drift fails verify-all"},
		{PipelineLefthook, "`praetorctl compile-context --verify` in lefthook pre-commit", "drift blocks commit"},
		{0, NotEnforced, Advisory},
	}
	for _, tc := range cases {
		if check, failure := drift.Adopted(tc.generated); check != tc.check || failure != tc.failure {
			t.Errorf("HISS-16 Adopted(%b) = %q, %q; want %q, %q", tc.generated, check, failure, tc.check, tc.failure)
		}
	}
	audit, _ := LookupRule("HISS-01")
	if check, failure := audit.Adopted(AllPipelines); check != "`praetorctl audit` HISS scan in verify-all + lefthook pre-commit/pre-push: Go `goto`, recursion + plain-function call cycles; Rust, Python direct recursion; C `goto`" ||
		failure != "new finding fails verify-all, blocks commit, blocks push" {
		t.Errorf("HISS-01 Adopted = %q, %q", check, failure)
	}
	ledger, _ := LookupRule("HISS-17")
	if check, failure := ledger.Adopted(AllPipelines); check != "`praetorctl state sync .` in lefthook post-commit" || failure != recordsOnly {
		t.Errorf("HISS-17 Adopted = %q, %q", check, failure)
	}
	if check, _ := ledger.Adopted(PipelineVerifyAll); check != NotEnforced {
		t.Errorf("HISS-17 credited to verify-all: %q", check)
	}
}

// TestAdoptedDirective_Negative_ComplexityMatchesTheAuditCeiling: the HISS-04 row states the
// caps of the audit ceiling (config.HISSComplexityCeiling) and, until a repository's own limit
// is resolved, that ceiling as the function length, so it cannot restate a stale value. The
// catalog may not import config (config's own tests import the catalog), so the numbers are
// pinned here.
func TestAdoptedDirective_Negative_ComplexityMatchesTheAuditCeiling(t *testing.T) {
	rule, _ := LookupRule("HISS-04")
	ceiling := config.HISSComplexityCeiling()
	if CeilingFuncLOC != ceiling.MaxFuncLOC {
		t.Fatalf("CeilingFuncLOC = %d, audit ceiling %d", CeilingFuncLOC, ceiling.MaxFuncLOC)
	}
	want := fmt.Sprintf("McCabe cyclomatic <= %d, cognitive <= %d, statements <= %d; func LOC <= %d (audit ceiling; stricter repository policy wins)",
		ceiling.MaxCyclomatic, ceiling.MaxCognitive, ceiling.MaxStatements, ceiling.MaxFuncLOC)
	if got := rule.AdoptedDirective(Facts{}); got != want {
		t.Fatalf("HISS-04 directive = %q, want %q", got, want)
	}
}
