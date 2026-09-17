package main

import "testing"

// The reason is mandatory in every form: an explicit flag wins, the environment reaches the
// generated pre-commit hook that runs bare "praetorctl audit", and whitespace is not a reason.
func TestResolveDebtDeltaReason_Positive_FlagThenEnvironment(t *testing.T) {
	t.Setenv(debtDeltaReasonEnv, "from environment")
	if got := resolveDebtDeltaReason("from flag"); got != "from flag" {
		t.Errorf("an explicit flag must win over the environment, got %q", got)
	}
	if got := resolveDebtDeltaReason(""); got != "from environment" {
		t.Errorf("the environment must reach a bare audit invocation, got %q", got)
	}
}

// Negative: the default is the boy-scout rule. Nothing set means no exception.
func TestResolveDebtDeltaReason_Negative_NothingSetMeansNoException(t *testing.T) {
	t.Setenv(debtDeltaReasonEnv, "")
	if got := resolveDebtDeltaReason(""); got != "" {
		t.Errorf("an exception was taken with no reason given: %q", got)
	}
}

// Boundary: a reason made only of whitespace is not a reason, in either form.
func TestResolveDebtDeltaReason_Boundary_WhitespaceIsNotAReason(t *testing.T) {
	t.Setenv(debtDeltaReasonEnv, "   \t ")
	if got := resolveDebtDeltaReason("  "); got != "" {
		t.Errorf("whitespace was accepted as a justification: %q", got)
	}
}
