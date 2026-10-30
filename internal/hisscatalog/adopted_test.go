// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
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
	supplyChain, _ := LookupRule("HISS-11")
	if check, failure := supplyChain.Adopted(AllPipelines); check != "`praetorctl audit` supply chain + image digest pins in verify-all + lefthook pre-commit/pre-push: Actions SHA pins, Python lock hashes, CDN imports unchecked" ||
		failure != "new finding fails verify-all, blocks commit, blocks push" {
		t.Fatalf("HISS-11 Adopted() = %q, %q", check, failure)
	}
	for _, id := range []string{"HISS-03", "HISS-05", "HISS-06"} {
		rule, _ := LookupRule(id)
		if text := rule.AdoptedExplanation(); !strings.Contains(text, "HISS scan in verify-all + lefthook pre-commit/pre-push") {
			t.Errorf("%s adopted explanation: %s", id, text)
		}
	}
	unchecked, _ := LookupRule("HISS-18")
	if text := unchecked.AdoptedExplanation(); text != unchecked.Explanation()+"\nAdopted repositories: not enforced; adoption generates no check for this rule." {
		t.Fatalf("HISS-18 adopted explanation:\n%s", text)
	}
	if check, failure := unchecked.Adopted(AllPipelines); check != NotEnforced || failure != Advisory {
		t.Fatalf("HISS-18 Adopted() = %q, %q", check, failure)
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

// TestAdoptedFor states each check only where it decides the repository's languages. Positive:
// a Go repository keeps the audit's HISS-09 scan and the pre-commit warning cascade, and a Rust
// crate keeps the zero-warnings cascade. Negative: a rule with no analogue (HISS-09 in C), a scan
// that reads none of the languages (HISS-01 in a TypeScript repository, HISS-07 in C) and
// pre-commit warnings in a Python repository read not enforced.
// Boundary: unknown languages keep every check as Adopted states it, a language-independent
// check (HISS-16) holds in every repository, and a mixed Go + TypeScript repository keeps Go's.
func TestAdoptedFor(t *testing.T) {
	cases := []struct {
		id        string
		languages Language
		enforced  bool
	}{
		{"HISS-05", LanguageGo, true},
		{"HISS-05", LanguageRust, false},
		{"HISS-05", LanguageGo | LanguageOther, true},
		{"HISS-09", LanguageGo, true},
		{"HISS-10", LanguageGo, true},
		{"HISS-09", LanguageC, false},
		{"HISS-01", LanguageOther, false},
		{"HISS-07", LanguageC, false},
		{"HISS-12", LanguageGo, true},
		{"HISS-12", LanguageRust, true},
		{"HISS-12", LanguagePython, true},
		{"HISS-12", LanguageC, true},
		{"HISS-12", LanguageOther, true},
		{"HISS-10", LanguageRust, true},
		{"HISS-10", LanguagePython, false},
		{"HISS-16", LanguageOther, true},
		{"HISS-10", LanguageGo | LanguageOther, true},
	}
	for _, tc := range cases {
		rule, _ := LookupRule(tc.id)
		check, failure := rule.AdoptedFor(AllPipelines, Facts{Languages: tc.languages})
		if enforced := check != NotEnforced; enforced != tc.enforced || (!enforced && failure != Advisory) {
			t.Errorf("%s for %b: %q, %q; want enforced=%v", tc.id, tc.languages, check, failure, tc.enforced)
		}
		assertHISS12AdoptedFor(t, tc.id, tc.languages, check)
	}
	for _, rule := range Rules() {
		wantCheck, wantFailure := rule.Adopted(AllPipelines)
		if check, failure := rule.AdoptedFor(AllPipelines, Facts{}); check != wantCheck || failure != wantFailure {
			t.Errorf("%s with unknown languages: %q, %q; want %q, %q", rule.ID, check, failure, wantCheck, wantFailure)
		}
		if rule.Adoption.Languages&^scannedLanguages != 0 && rule.Adoption.Check == adoptedAudit {
			t.Errorf("%s audit scan claims a language internal/hiss does not read: %b", rule.ID, rule.Adoption.Languages)
		}
	}
}

func assertHISS12AdoptedFor(t *testing.T, id string, languages Language, check string) {
	t.Helper()
	if id == "HISS-12" && !strings.Contains(check, "`praetorctl security secrets` (gitleaks history scan)") {
		t.Errorf("HISS-12 for %b: %q; want security secrets check text", languages, check)
	}
}

func TestAuditScanner_PositiveNegativeBoundary(t *testing.T) {
	scanner, ok := LookupRule("HISS-01")
	if !ok {
		t.Fatal("HISS-01 missing from catalog")
	}
	if got := scanner.AuditScanner(Facts{Languages: LanguageGo}); got == "" {
		t.Fatal("Go audit scanner not reported for HISS-01")
	}
	if got := scanner.AuditScanner(Facts{Languages: LanguageOther}); got != "" {
		t.Errorf("scanner applies to other-language repository: %q", got)
	}
	for _, id := range []string{"HISS-03", "HISS-05", "HISS-06"} {
		rule, _ := LookupRule(id)
		if got := rule.AuditScanner(Facts{Languages: LanguageGo}); got == "" {
			t.Errorf("Go audit scanner missing for %s", id)
		}
	}
	unknown, ok := LookupRule("HISS-18")
	if !ok {
		t.Fatal("HISS-18 missing from catalog")
	}
	if got := unknown.AuditScanner(Facts{Languages: LanguageGo}); got != "" {
		t.Errorf("non-scanner rule acquired audit scanner: %q", got)
	}
	if got := scanner.AuditScanner(Facts{}); got == "" {
		t.Error("unknown language set must retain possible scanner")
	}
}
