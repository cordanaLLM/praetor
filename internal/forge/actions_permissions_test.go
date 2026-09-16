package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func declared(write bool, prs bool) config.ActionsPolicy {
	perm := "read"
	if write {
		perm = "write"
	}
	return config.ActionsPolicy{DefaultWorkflowPermissions: perm, AllowCreateAndApprovePullRequests: prs}
}

func live(perm string, prs bool) WorkflowPermissions {
	return WorkflowPermissions{DefaultWorkflowPermissions: perm, CanApprovePullRequestReviews: prs}
}

// Positive (#153 acceptance): a repository declaring pull-request creation that has it, under an
// organisation that permits it, is compliant.
func TestEvaluateActionsPermissions_Positive_CompliantWhenLiveMatchesDeclaration(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, true), live("read", true), live("read", true))
	if got.Verdict != ActionsCompliant {
		t.Fatalf("matching declaration reported %s: %s", got.Verdict, got.Detail)
	}
}

// Negative (#153 acceptance), measured on this organisation today: praetor declares false, the
// organisation raised its ceiling to true, and a repository that never pinned the value inherits
// true. It must be reported as drift with the organisation named as the source.
func TestEvaluateActionsPermissions_Negative_InheritedDriftNamesTheOrganisation(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, false), live("read", true), live("read", true))
	if got.Verdict != ActionsDriftedByOrganisation {
		t.Fatalf("inherited drift reported as %s: %s", got.Verdict, got.Detail)
	}
	if !strings.Contains(got.Detail, "organisation") || !strings.Contains(got.Detail, "pin") {
		t.Errorf("the finding does not name the organisation as the source or say to pin: %s", got.Detail)
	}
}

// Negative: a repository set to something neither its declaration nor its organisation holds was
// changed on the repository itself, which is a different fix and must not blame the organisation.
func TestEvaluateActionsPermissions_Negative_RepositoryLevelDriftIsNotBlamedOnTheOrganisation(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, false), live("write", false), live("read", false))
	if got.Verdict != ActionsDriftedAtRepository {
		t.Fatalf("repository-level drift reported as %s: %s", got.Verdict, got.Detail)
	}
}

// Boundary (#153 acceptance): declaring true under an organisation that denies it is unsatisfiable
// at the repository. It must name the organisation as the blocker instead of surfacing the bare
// 409 Conflict a reconcile would return.
func TestEvaluateActionsPermissions_Boundary_DeclaredCapabilityDeniedByOrganisation(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, true), live("read", false), live("read", false))
	if got.Verdict != ActionsBlockedByOrganisation {
		t.Fatalf("a capability the organisation denies was reported as %s: %s", got.Verdict, got.Detail)
	}
	if strings.Contains(got.Detail, "409") || !strings.Contains(got.Detail, "organisation") {
		t.Errorf("the blocker must be named, not reported as a status code: %s", got.Detail)
	}
}

// Boundary: the blocker check takes precedence. A repository that happens to already agree with
// an organisation that denies the declared capability is still blocked, not compliant.
func TestEvaluateActionsPermissions_Boundary_BlockerOutranksAgreement(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, true), live("read", true), live("read", false))
	if got.Verdict != ActionsBlockedByOrganisation {
		t.Fatalf("a denied capability was reported as %s", got.Verdict)
	}
}

// Boundary: the permissions level is compared as well as the pull-request flag, so write where
// read is declared is drift even when the flag agrees.
func TestEvaluateActionsPermissions_Boundary_PermissionLevelAloneIsDrift(t *testing.T) {
	got := EvaluateActionsPermissions(declared(false, false), live("write", false), live("write", false))
	if got.Verdict == ActionsCompliant {
		t.Fatal("write permissions were accepted where read is declared")
	}
}
