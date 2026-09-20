package readmegovernance

import (
	"errors"
	"strings"
	"testing"
)

func TestReconcilePositiveRendersTruthfulRecordedDebt(t *testing.T) {
	out, changed, err := Reconcile("# Demo\n\nHuman text.\n", State{BaselineKnown: true, LegacyDebtCount: 2})
	if err != nil || !changed {
		t.Fatalf("reconcile: changed=%v err=%v", changed, err)
	}
	for _, want := range []string{Start, End, "2%20baselined", "2 recorded infractions", "Human text."} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered README missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Compliant") || strings.Contains(out, "conforms") {
		t.Fatalf("adoption state was rendered as certification:\n%s", out)
	}
	again, changed, err := Reconcile(out, State{BaselineKnown: true, LegacyDebtCount: 2})
	if err != nil || changed || again != out {
		t.Fatalf("reconcile is not idempotent: changed=%v err=%v\n%s", changed, err, again)
	}
}

func TestReconcileNegativeRejectsInvalidAndMalformedInputs(t *testing.T) {
	if _, _, err := Reconcile("# Demo\n", State{LegacyDebtCount: -1}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("negative count: %v", err)
	}
	malformed := "# Demo\n\n" + Start + "\nunterminated\n"
	if _, _, err := Reconcile(malformed, State{}); err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed markers: %v", err)
	}
}

func TestReconcileBoundaryPreservesCustomBadgeAndMigratesLegacyOutput(t *testing.T) {
	custom := "[![HISS policy](https://img.shields.io/badge/Custom-HISS-blue)](policy.md)"
	legacy := "[![HISS-16 Compliant](https://img.shields.io/badge/Standards-HISS--16%20Compliant-brightgreen)](AGENTS.md)"
	input := custom + "\n" + legacy + legacyGovernanceHISS16 + "\nHuman tail.\n"
	out, _, err := Reconcile(input, State{BaselineKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, custom) != 1 || strings.Contains(out, legacy) || strings.Contains(out, "standardsctl audit") {
		t.Fatalf("legacy migration changed custom content or retained generated output:\n%s", out)
	}
	if strings.Contains(out, "Standards-HISS%20Adopted") {
		t.Fatalf("custom HISS badge was duplicated by a managed badge:\n%s", out)
	}
	if err := Verify(out, State{BaselineKnown: true}); err != nil {
		t.Fatalf("verified reconciled output: %v", err)
	}
}

func TestReconcileBoundaryMigratesEveryShippedFalseComplianceBadge(t *testing.T) {
	cases := map[string]string{
		"hiss-16": "[![HISS-16 Compliant](https://img.shields.io/badge/Standards-HISS--16%20Compliant-brightgreen)](AGENTS.md)" + legacyGovernanceHISS16,
		"current": "[![HISS Compliant](https://img.shields.io/badge/Standards-HISS%20Compliant-brightgreen)](AGENTS.md)" + legacyGovernanceCurrent,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := Reconcile("# Demo\n\n"+input+"\nHuman tail.\n", State{BaselineKnown: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, stale := range []string{"Compliant", "This repository conforms", "standardsctl audit"} {
				if strings.Contains(out, stale) {
					t.Fatalf("legacy %s output retained %q:\n%s", name, stale, out)
				}
			}
			if !strings.Contains(out, "HISS%20Adopted-blue") || !strings.Contains(out, "Human tail.") {
				t.Fatalf("legacy %s output was not migrated safely:\n%s", name, out)
			}
		})
	}
}

func TestVerifyBoundaryDistinguishesMissingAndStale(t *testing.T) {
	if err := Verify("# Demo\n", State{}); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing block: %v", err)
	}
	out, _, err := Reconcile("# Demo\n", State{BaselineKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(out, "0 recorded infractions", "9 recorded infractions", 1)
	if err := Verify(stale, State{BaselineKnown: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale block: %v", err)
	}
}

func TestReconcilePositiveAcceptsSynchronizedCRLF(t *testing.T) {
	state := State{BaselineKnown: true, LegacyDebtCount: 2}
	lf, _, err := Reconcile("# Demo\n\nHuman text.\n", state)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if err := Verify(crlf, state); err != nil {
		t.Fatalf("verify synchronized CRLF README: %v", err)
	}
	again, changed, err := Reconcile(crlf, state)
	if err != nil || changed || again != crlf {
		t.Fatalf("reconcile synchronized CRLF README: changed=%v err=%v\n%q", changed, err, again)
	}
}

func TestReconcileNegativeRepairsStaleCRLFWithoutMixingEndings(t *testing.T) {
	state := State{BaselineKnown: true}
	lf, _, err := Reconcile("# Demo\n\nHuman text.\n", state)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	stale := strings.Replace(crlf, "0 recorded infractions", "9 recorded infractions", 1)
	if err := Verify(stale, state); !errors.Is(err, ErrStale) {
		t.Fatalf("verify stale CRLF README: error=%v, want %v", err, ErrStale)
	}
	repaired, changed, err := Reconcile(stale, state)
	if err != nil || !changed {
		t.Fatalf("repair stale CRLF README: changed=%v err=%v", changed, err)
	}
	assertOnlyCRLF(t, repaired)
	if !strings.Contains(repaired, "Human text.\r\n") {
		t.Fatalf("repair did not preserve human content: %q", repaired)
	}
}

func TestReconcileBoundaryInsertsBlockIntoCRLFWithoutTerminalNewline(t *testing.T) {
	input := "# Demo\r\n\r\nHuman text."
	out, changed, err := Reconcile(input, State{})
	if err != nil || !changed {
		t.Fatalf("insert into CRLF README: changed=%v err=%v", changed, err)
	}
	assertOnlyCRLF(t, out)
	if !strings.Contains(out, "Human text.\r\n") {
		t.Fatalf("insert did not preserve human content: %q", out)
	}
	if err := Verify(out, State{}); err != nil {
		t.Fatalf("verify reconciled CRLF README: %v", err)
	}
}

func assertOnlyCRLF(t *testing.T, content string) {
	t.Helper()
	if strings.Count(content, "\n") != strings.Count(content, "\r\n") {
		t.Fatalf("README contains mixed line endings: %q", content)
	}
}
