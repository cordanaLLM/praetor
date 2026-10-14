package agentcontext

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// adoptedSource returns an unmarked canonical AGENTS.md: an engine block of engine lines (lint
// comment, harness H1, rules, end marker) followed by adopter lines of repository text.
func adoptedSource(engine, adopter int) string {
	lines := []string{"<!-- markdownlint-disable MD013 -->", "# acme/widget " + HarnessTitleSuffix}
	for i := 0; i < engine-3; i++ {
		lines = append(lines, "- engine rule.")
	}
	lines = append(lines, HarnessEndMarker)
	for i := 0; i < adopter; i++ {
		lines = append(lines, "- adopter rule.")
	}
	return strings.Join(lines, "\n") + "\n"
}

// compileErr compiles content with every projection selected.
func compileErr(content string) error {
	_, err := NewTranspiler().CompileContent(content)
	return err
}

// #1232: an unchanged adopter AGENTS.md (213 lines) failed once the engine block grew from 75
// to 104 lines. The growth above EngineBlockReserve is not charged to adopter text.
func TestCompileContentEngineBlockGrowthNotChargedToAdopter(t *testing.T) {
	if err := compileErr(adoptedSource(104, 213)); err != nil {
		t.Fatalf("positive: unchanged adopter text below the grown engine block rejected: %v", err)
	}
	// Boundary: the largest projection (Cursor) carries 10 lines beyond the source; with 104
	// engine lines, 29 of them exempt, 215 adopter lines land on the budget exactly.
	if err := compileErr(adoptedSource(104, 215)); err != nil {
		t.Fatalf("boundary: adopter text at its allowance rejected: %v", err)
	}
	err := compileErr(adoptedSource(104, 216))
	if err == nil {
		t.Fatal("boundary: adopter text one line over its allowance accepted")
	}
	for _, want := range []string{"exceeds max line budget (330 > 300", "engine block 104 lines", "adopter text 226 lines, adopter allowance 225"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("over-budget error %q lacks %q", err, want)
		}
	}
	if err := compileErr(adoptedSource(104, 260)); err == nil {
		t.Fatal("negative: adopter text far over its allowance accepted")
	}
}

// The budget of a file without a harness is the plain MaxLineBudget, and engine growth past
// EngineBlockMaxLines is charged again, so neither is a way around the budget.
func TestCompileContentBudgetWithoutExemption(t *testing.T) {
	plain := func(n int) string { return strings.Repeat("- rule.\n", n) }
	if err := compileErr(plain(290)); err != nil {
		t.Fatalf("boundary: plain file at the budget rejected: %v", err)
	}
	err := compileErr(plain(291))
	if err == nil || !strings.Contains(err.Error(), "(301 > 300)") {
		t.Fatalf("negative: plain file one line over: error = %v, want (301 > 300) without engine parts", err)
	}
	noTitle := strings.Replace(adoptedSource(104, 200), "# acme/widget "+HarnessTitleSuffix, "# Rules", 1)
	if err := compileErr(noTitle); err == nil {
		t.Fatal("negative: end marker without a harness H1 earned the engine exemption")
	}
	if err := compileErr(adoptedSource(130, 205)); err != nil {
		t.Fatalf("boundary: engine block over its allowance, adopter at the reduced limit rejected: %v", err)
	}
	if err := compileErr(adoptedSource(130, 206)); err == nil {
		t.Fatal("negative: engine lines past EngineBlockMaxLines went uncharged")
	}
}

func TestCheckContextBudgetCountsAdopterText(t *testing.T) {
	if err := CheckContextBudget(adoptedSource(104, 225)); err != nil {
		t.Fatalf("boundary: 225 adopter lines below a 104-line engine block rejected: %v", err)
	}
	err := CheckContextBudget(adoptedSource(104, 226))
	if !errors.Is(err, util.ErrMarkedBlockBudget) || !strings.Contains(err.Error(), "330 lines, budget 300; engine block 104 lines") ||
		!strings.Contains(err.Error(), "adopter text 226 lines, adopter allowance 225") {
		t.Fatalf("boundary: one adopter line over: error = %v", err)
	}
	if err := CheckContextBudget(strings.Repeat("x\n", MaxLineBudget+1)); !errors.Is(err, util.ErrMarkedBlockBudget) || strings.Contains(err.Error(), "engine block") {
		t.Fatalf("negative: plain file over budget: error = %v", err)
	}
}

func TestEngineOwnedLines(t *testing.T) {
	section := "\n" + RegisterBlockHeading + "\n\n" + RegisterBlockStart + "\nrow\n" + RegisterBlockEnd + "\n"
	cases := []struct {
		name    string
		content string
		want    int
	}{
		{"positive: harness through the end marker", adoptedSource(104, 3), 104},
		{"positive: register section appended below the harness", adoptedSource(75, 3) + section, 75 + 6},
		{"positive: preamble above the lint comment stays adopter text", "SPDX\n" + adoptedSource(80, 0), 80},
		{"negative: no harness", "# Rules\n" + HarnessEndMarker + "\n", 0},
		{"negative: harness without end marker", "# acme " + HarnessTitleSuffix + "\nrule\n", 0},
		{"negative: end marker inside a fence", "# acme " + HarnessTitleSuffix + "\n```\n" + HarnessEndMarker + "\n```\n", 0},
		{"boundary: H1 and end marker only", "# acme " + HarnessTitleSuffix + "\n" + HarnessEndMarker, 2},
	}
	for _, tc := range cases {
		if got := EngineOwnedLines(tc.content); got != tc.want {
			t.Errorf("%s: EngineOwnedLines = %d, want %d", tc.name, got, tc.want)
		}
	}
}
