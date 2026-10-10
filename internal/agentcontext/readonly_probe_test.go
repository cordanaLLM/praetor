package agentcontext

import (
	"strings"
	"testing"
)

// adopterHarnessWithLedgerCheck is the adopted harness shape whose HISS-17 row carries the
// generated `praetorctl state sync .` check in its Adopted check column (hisscatalog HISS-17,
// StagePostCommit), the row that made adoption abort before the projection filtered every cell.
const adopterHarnessWithLedgerCheck = "# acme/widget Agent Operating Harness\n\n" +
	"Before concluding any turn:\n\n```bash\nmake verify-all\n```\n\n" +
	"`make verify-all` = repository gate. Steps live in `Makefile`.\nPass = exit 0.\n\n" +
	"## Core Directives & Invariants\n\n" +
	"Adopted check = check adoption generated here (generated `make verify-all` + praetor `lefthook.yml`).\n\n" +
	"| Invariant | Rule | Adopted check | On fail |\n| :--- | :--- | :--- | :--- |\n" +
	"| **HISS-16** context integrity | single canonical `AGENTS.md` | `praetorctl compile-context --verify` in verify-all | drift fails verify-all |\n" +
	"| **HISS-17** state ledger | turn start `praetorctl state status`; turn end `praetorctl state sync .` | `praetorctl state sync .` in post-commit | hook fails |\n"

// TestReadOnlyProjection_Negative_EveryCellOfALedgerRowIsFiltered: blocker 1. A mutating
// command in any column of a table row is dropped clause by clause, never an error.
func TestReadOnlyProjection_Negative_EveryCellOfALedgerRowIsFiltered(t *testing.T) {
	got, err := ReadOnlyProjection(adopterHarnessWithLedgerCheck)
	if err != nil {
		t.Fatalf("projection of an adopted ledger check failed: %v", err)
	}
	if strings.Contains(got, "state sync") || strings.Contains(got, "make verify-all") {
		t.Fatalf("mutating command survived:\n%s", got)
	}
	for _, want := range []string{
		"| **HISS-17** state ledger | turn start `praetorctl state status` | read-only run: step dropped | hook fails |",
		"| **HISS-16** context integrity | single canonical `AGENTS.md` | `praetorctl compile-context --verify` in verify-all | drift fails verify-all |",
		"Pass = exit 0.",
		"(generated verification gate + praetor `lefthook.yml`)",
	} {
		if strings.Contains(want, "Pass = exit 0.") {
			if strings.Contains(got, want) {
				t.Errorf("turn-end paragraph naming the dropped step survived:\n%s", got)
			}
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// TestReadOnlyProjection_Positive_ProhibitionsAndUnrelatedRulesSurvive: major 4. Prohibitions
// that name a mutating command, prose that only resembles one, and a block an adopter added
// after the turn-end steps all survive; only the step block and its own paragraph go.
func TestReadOnlyProjection_Positive_ProhibitionsAndUnrelatedRulesSurvive(t *testing.T) {
	input := "# Harness\n\nBefore concluding any turn:\n```bash\nmake verify-all\n```\n\n" +
		"### Important rules\n\n- Never delete user data.\n- Keep secrets out of the repository.\n\n" +
		"## Operational Rules\n\n1. **Git hygiene.** Review diffs.\n" +
		"   - Never `git push --force` to main.\n   - Never run `git add -A` blindly.\n" +
		"   - Keep state synchronization docs current.\n   - Turn end: run `git commit -s`.\n"
	got, err := ReadOnlyProjection(input)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	for _, want := range []string{
		"### Important rules", "- Never delete user data.", "- Keep secrets out of the repository.",
		"- Never `git push --force` to main.", "- Never run `git add -A` blindly.",
		"- Keep state synchronization docs current.", "1. **Git hygiene.** Review diffs.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dropped %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"Turn end: run", "make verify-all", "Before concluding any turn:"} {
		if strings.Contains(got, gone) {
			t.Errorf("kept %q:\n%s", gone, got)
		}
	}
}

// TestReadOnlyProjection_Boundary_LedgerRuleAtAnyNumber: major 5. The ledger rule numbered 5
// keeps rules 6 and 7 and their blank lines; the note for its dropped step sits under rule 5,
// nothing under rule 7; a lead sentence holding a mutating command is dropped from the lead,
// its other sentences kept.
func TestReadOnlyProjection_Boundary_LedgerRuleAtAnyNumber(t *testing.T) {
	input := "# Harness\n\n## Operational Rules\n\n" +
		"5. **State ledger discipline (HISS-17).** Turn end: run `praetorctl state sync .` always. Keep ledger private.\n" +
		"   - Turn start: `praetorctl state status`.\n   - During work: `praetorctl state task add \"x\"`.\n\n" +
		"6. **Six.** Rule six text.\n\n7. **Seven.** Rule seven text.\n"
	got, err := ReadOnlyProjection(input)
	if err != nil {
		t.Fatalf("projection failed: %v", err)
	}
	want := "5. **State ledger discipline (HISS-17).** Keep ledger private.\n" +
		"   - Turn start: `praetorctl state status`.\n   - " + readOnlyItemNote + "\n\n" +
		"6. **Six.** Rule six text.\n\n7. **Seven.** Rule seven text.\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("ledger rule block:\n%s\nwant suffix:\n%s", got, want)
	}
}
