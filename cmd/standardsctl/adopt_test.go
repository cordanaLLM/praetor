package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

func TestParseAdoptArgs_Positive(t *testing.T) {
	fx := parseAdoptFixture(t, "repo-dir", "--profile", "framework", "--dry-run")
	if fx.err != nil || fx.profile != "framework" || !fx.bools["dry-run"] || strings.Join(fx.positional, " ") != "repo-dir" {
		t.Fatalf("flags after the path must bind and the path stay positional: %+v", fx)
	}
}

func TestParseAdoptArgs_Negative_BoolFlagsNeverConsumeAPath(t *testing.T) {
	for _, flagName := range []string{"--dry-run", "-force", "--record-baseline", "--all-missing"} {
		fx := parseAdoptFixture(t, flagName, "repo-dir")
		if fx.err != nil || strings.Join(fx.positional, " ") != "repo-dir" || !fx.bools[strings.TrimLeft(flagName, "-")] {
			t.Errorf("%s must not swallow the positional path: %+v", flagName, fx)
		}
	}
	if fx := parseAdoptFixture(t, "repo-dir", "--profile"); fx.err == nil {
		t.Errorf("a trailing value flag has no value and must be rejected: %+v", fx)
	}
}

func TestParseAdoptArgs_Boundary(t *testing.T) {
	if fx := parseAdoptFixture(t); fx.err != nil || len(fx.positional) != 0 {
		t.Fatalf("no args must yield no positionals: %+v", fx)
	}
	if fx := parseAdoptFixture(t, "--profile=framework", "repo"); fx.err != nil || fx.profile != "framework" || strings.Join(fx.positional, " ") != "repo" {
		t.Fatalf("flag=value form must stay intact: %+v", fx)
	}
	// The flag package reads the next token as a value flag's value, whatever it looks like.
	if fx := parseAdoptFixture(t, "--profile", "--dry-run"); fx.err != nil || fx.profile != "--dry-run" || fx.bools["dry-run"] {
		t.Fatalf("a value flag takes the next token as its value: %+v", fx)
	}
}

func TestSplitFacets_3D(t *testing.T) {
	if got := splitCommaList("a:b, c:d ,,"); len(got) != 2 || got[0] != "a:b" || got[1] != "c:d" {
		t.Fatalf("positive: got %v", got)
	}
	if got := splitCommaList(""); len(got) != 0 {
		t.Fatalf("negative: empty input must yield no facets, got %v", got)
	}
	if got := splitCommaList(" , "); len(got) != 0 {
		t.Fatalf("boundary: whitespace-only entries are dropped, got %v", got)
	}
}

func TestPrintBatchResult_ReportsErrorsAsFailure(t *testing.T) {
	ok := printBatchResult("r", false, &adopt.AdoptReport{Errors: []string{"boom"}}, nil)
	if ok {
		t.Fatal("a report with errors is a failed adoption")
	}
	if printBatchResult("r", false, nil, errors.New("hard failure")) {
		t.Fatal("an error is a failed adoption")
	}
	if !printBatchResult("r", true, &adopt.AdoptReport{Warnings: []string{"soft"}}, nil) {
		t.Fatal("warnings alone do not fail an adoption")
	}
}

func TestPrintBatchResult_ShowsTheRepositoryNameInSlashForm(t *testing.T) {
	// Positive: a Windows scan's backslash name renders in slash form on success and failure.
	out, err := captureStdout(t, func() error {
		printBatchResult(`acme\widgets`, true, &adopt.AdoptReport{}, nil)
		printBatchResult(`acme\gadgets`, true, nil, errors.New("no lock source"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[ADOPTED] acme/widgets ", "[FAIL] acme/gadgets: no lock source")
	// Negative and boundary: a name without separators, and the empty name, pass through.
	out, err = captureStdout(t, func() error {
		printBatchResult("solo", true, &adopt.AdoptReport{}, nil)
		printBatchResult("", true, nil, errors.New("empty"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "[ADOPTED] solo ", "[FAIL] : empty")
}

func TestPrintAdoptReportDoesNotClaimUnobservedBaselineOrPillars(t *testing.T) {
	out, err := captureStdout(t, func() error {
		printAdoptReport(&adopt.AdoptReport{State: adopt.StatePartial, BaselineStatus: "not_run", Errors: []string{"pre-baseline failure"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "baseline not_run; no usable baseline result") || strings.Contains(out, "Pillars Synchronized") {
		t.Fatalf("false-success diagnostics: %s", out)
	}
	out, err = captureStdout(t, func() error {
		printAdoptReport(&adopt.AdoptReport{DryRun: true, BaselineStatus: "scanned"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0 infractions (Scanned (dry-run; not written))") || !strings.Contains(out, "Pillars Planned") {
		t.Fatalf("dry-run diagnostics: %s", out)
	}
	out, err = captureStdout(t, func() error {
		printAdoptReport(&adopt.AdoptReport{DryRun: true, BaselineStatus: "scanned", LegacyDebtCount: 2, DebtBreakdown: map[string]int{"HISS-01": 2}, CreatedFiles: []string{"new.md"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Baselined") || strings.Contains(out, "recorded into") || strings.Contains(out, "Files Created") {
		t.Fatalf("dry-run claimed writes: %s", out)
	}
}

// BUG-871: the pillar lines follow the report. A DevContainer step that warned no longer
// prints a success mark, a step that never ran says so, and a clean step keeps its mark.
func TestPrintAdoptReportDerivesPillarLinesFromSteps(t *testing.T) {
	rep := &adopt.AdoptReport{
		BaselineStatus: "scanned",
		Warnings:       []string{"DevContainer bootstrap unavailable: no runtime"},
		Steps: []adopt.StepOutcome{
			{Name: "agent-harness", Status: adopt.StepCompleted},
			{Name: "dev-container", Status: adopt.StepCompleted, Warnings: []string{"DevContainer bootstrap unavailable: no runtime"}},
			{Name: "editors", Status: adopt.StepCompleted},
		},
	}
	out, err := captureStdout(t, func() error { printAdoptReport(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Governance Pillars Synchronized", "✓ Universal Harness", "✓ IDE Ecosystem",
		"⚠ DevContainer", "[warned: 1 warning(s)]", "Verification Gate", "[not-run]")
	if strings.Contains(out, "✓ DevContainer") || strings.Contains(out, "✓ Verification Gate") {
		t.Fatalf("a warned or unreached pillar printed a success mark:\n%s", out)
	}

	// Boundary: a dry run with errors reads incomplete, never planned.
	rep.DryRun, rep.Errors = true, []string{"makefile failed"}
	out, err = captureStdout(t, func() error { printAdoptReport(rep); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Governance Pillars Not Synchronized", "finished with 1 error(s)")
	if strings.Contains(out, "Pillars Planned") || strings.Contains(out, "Simulated adoption plan completed") {
		t.Fatalf("a failed dry run was reported as a plan:\n%s", out)
	}
}

// #594: an applied run whose verification plan is unavailable warns the Verification Gate and
// ends with a qualified line naming it, never the unqualified success line; a clean applied run
// keeps that line, and neither a declined pillar nor a warned DevContainer qualifies it.
func TestPrintAdoptReportQualifiesSuccessWithPendingPillars(t *testing.T) {
	allSteps := func(declined string) []adopt.StepOutcome {
		steps := make([]adopt.StepOutcome, 0, 4)
		for _, name := range []string{"agent-harness", "editors", "dev-container", "makefile"} {
			status := adopt.StepCompleted
			if name == declined {
				status = adopt.StepDeclined
			}
			steps = append(steps, adopt.StepOutcome{Name: name, Status: status})
		}
		return steps
	}
	unavailable := &adopt.AdoptReport{
		BaselineStatus: "scanned", Steps: allSteps(""),
		Verification: &adopt.VerificationPlan{Status: "unavailable", Reasons: []string{"A required build or test command is absent."}},
	}
	out, err := captureStdout(t, func() error { printAdoptReport(unavailable); return nil })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "⚠ Verification Gate", "[warned: 1 warning(s)]", "not ready yet: Verification Gate. See the warnings above.")
	if strings.Contains(out, "successfully adopted") || strings.Contains(out, "✓ Verification Gate") {
		t.Fatalf("unavailable verification reported as a clean adoption:\n%s", out)
	}
	preserved := allSteps("")
	preserved[2].Warnings = []string{"Existing DevContainer preserved; bootstrap readiness requires separate verification."}
	for _, rep := range []*adopt.AdoptReport{
		{BaselineStatus: "scanned", Steps: allSteps(""), Verification: &adopt.VerificationPlan{Status: "declared-unverified"}},
		{BaselineStatus: "scanned", Steps: allSteps("editors")},
		{BaselineStatus: "scanned", Steps: preserved, Verification: &adopt.VerificationPlan{Status: "declared-unverified"}},
	} {
		out, err = captureStdout(t, func() error { printAdoptReport(rep); return nil })
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Repository successfully adopted") || strings.Contains(out, "not ready yet") {
			t.Fatalf("clean adoption qualified:\n%s", out)
		}
	}
}

// adoptArgsFixture is what parseInterspersed made of one adoption argv.
type adoptArgsFixture struct {
	profile    string
	bools      map[string]bool
	positional []string
	err        error
}

// parseAdoptFixture parses args with a FlagSet carrying the adoption command's boolean
// flags and its --profile value flag, through the parser runAdopt uses.
func parseAdoptFixture(t *testing.T, args ...string) adoptArgsFixture {
	t.Helper()
	fs := newQuietFlagSet("adopt")
	profile := fs.String("profile", "", "")
	bools := map[string]*bool{}
	for _, name := range []string{"dry-run", "force", "record-baseline", "all-missing"} {
		bools[name] = fs.Bool(name, false, "")
	}
	positional, err := parseInterspersed(fs, args)
	fx := adoptArgsFixture{profile: *profile, bools: map[string]bool{}, positional: positional, err: err}
	for name, value := range bools {
		fx.bools[name] = *value
	}
	return fx
}
