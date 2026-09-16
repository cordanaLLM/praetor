package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: a missing baseline is named as the cause, with the command that fixes it, instead of
// the ratchet's "N new unbaselined" -- which blames code that predates the change (#138).
func TestMissingBaselineFailure_Positive_NamesTheCauseAndTheFix(t *testing.T) {
	err := missingBaselineFailure(".standards-baseline.json", 221)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"no baseline exists", "221", "not a regression", "baseline --record"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}
}

// End to end through the function the fix lives in, against a real HISS scan. A repository with
// real debt and no baseline must fail naming the missing baseline -- not "N new unbaselined".
func TestAuditBaselineAndInvariants_Negative_BrownfieldWithoutBaselineNamesTheCause(t *testing.T) {
	root := t.TempDir()
	writeAuditFixture(t, root, "debt.go", "package main\n\nfunc loop() {\n\tfor {\n\t}\n}\n")
	err := auditBaselineAndInvariants(t.Context(), &auditOptions{
		rootDir:      root,
		baselinePath: filepath.Join(root, ".standards-baseline.json"),
		touched:      []string{"README.md"},
	})
	if err == nil {
		t.Fatal("a repository with debt and no baseline passed the audit")
	}
	if !strings.Contains(err.Error(), "no baseline exists") {
		t.Fatalf("the failure blames the code instead of the missing baseline: %v", err)
	}
}

// Boundary: a greenfield repository has no baseline and no debt, and that is a genuine zero. The
// missing-baseline message must not fire for it -- it has nothing to record.
func TestAuditBaselineAndInvariants_Boundary_GreenfieldWithoutBaselinePasses(t *testing.T) {
	root := t.TempDir()
	writeAuditFixture(t, root, "clean.go", "package main\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n")
	err := auditBaselineAndInvariants(t.Context(), &auditOptions{
		rootDir:      root,
		baselinePath: filepath.Join(root, ".standards-baseline.json"),
		touched:      []string{"README.md"},
	})
	if err != nil {
		t.Fatalf("a clean greenfield repository without a baseline failed: %v", err)
	}
}

func writeAuditFixture(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
