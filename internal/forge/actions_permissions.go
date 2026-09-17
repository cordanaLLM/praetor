package forge

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
)

// WorkflowPermissions is the live value of a repository's or organisation's Actions workflow
// permissions, as GET .../actions/permissions/workflow returns it.
type WorkflowPermissions struct {
	DefaultWorkflowPermissions   string `json:"default_workflow_permissions"`
	CanApprovePullRequestReviews bool   `json:"can_approve_pull_request_reviews"`
}

// ActionsVerdict classifies a repository's live workflow permissions against its declaration.
type ActionsVerdict string

const (
	// ActionsCompliant means the live value agrees with the declaration.
	ActionsCompliant ActionsVerdict = "compliant"
	// ActionsDriftedByOrganisation means the repository differs from its declaration and
	// matches its organisation, so the organisation is the likely source: a repository that
	// never set the value inherits it, and raising an organisation ceiling silently grants the
	// capability to every repository that never pinned it.
	ActionsDriftedByOrganisation ActionsVerdict = "drifted-by-organisation"
	// ActionsDriftedAtRepository means the repository differs from both its declaration and its
	// organisation, so the value was set on the repository itself.
	ActionsDriftedAtRepository ActionsVerdict = "drifted-at-repository"
	// ActionsBlockedByOrganisation means the repository declares a capability the organisation
	// denies. It cannot be granted at the repository at all; the organisation is the blocker.
	ActionsBlockedByOrganisation ActionsVerdict = "blocked-by-organisation"
)

// ActionsFinding is one decided comparison, with the reason a reader needs.
type ActionsFinding struct {
	Verdict ActionsVerdict
	Detail  string
}

// EvaluateActionsPermissions compares a declaration with the live repository and organisation
// values and names the source of any disagreement.
//
// The API returns the effective value and never says whether it was inherited, so the
// organisation is identified by comparison: a repository that agrees with its organisation and
// not with its declaration most plausibly never set the value. That distinction is the point of
// the audit. Reporting a bare mismatch would send a reader to change the repository, which fails
// with 409 Conflict while the organisation denies it, and which is not where the drift came from
// when the organisation raised a ceiling on everyone (#153).
func EvaluateActionsPermissions(declared config.ActionsPolicy, repo, org WorkflowPermissions) ActionsFinding {
	// A declared capability the organisation denies is checked first: nothing done at the
	// repository can satisfy it, so it must not be reported as ordinary drift.
	if declared.AllowCreateAndApprovePullRequests && !org.CanApprovePullRequestReviews {
		return ActionsFinding{Verdict: ActionsBlockedByOrganisation,
			Detail: "the repository declares allow_create_and_approve_pull_requests: true, but the " +
				"organisation denies it; the repository setting cannot be raised above the " +
				"organisation, so this must be changed at the organisation"}
	}
	if agrees(declared, repo) {
		return ActionsFinding{Verdict: ActionsCompliant, Detail: "live workflow permissions match the declaration"}
	}
	if repo == org {
		return ActionsFinding{Verdict: ActionsDriftedByOrganisation,
			Detail: fmt.Sprintf("live value %s matches the organisation, not the declaration %s; "+
				"the repository is inheriting the organisation setting and must pin its declared value",
				describe(repo), describeDeclared(declared))}
	}
	return ActionsFinding{Verdict: ActionsDriftedAtRepository,
		Detail: fmt.Sprintf("live value %s differs from both the declaration %s and the organisation %s; "+
			"it was set on the repository", describe(repo), describeDeclared(declared), describe(org))}
}

func agrees(declared config.ActionsPolicy, live WorkflowPermissions) bool {
	return declared.DefaultWorkflowPermissions == live.DefaultWorkflowPermissions &&
		declared.AllowCreateAndApprovePullRequests == live.CanApprovePullRequestReviews
}

func describe(p WorkflowPermissions) string {
	return fmt.Sprintf("{permissions=%s, create/approve PRs=%t}", p.DefaultWorkflowPermissions, p.CanApprovePullRequestReviews)
}

func describeDeclared(p config.ActionsPolicy) string {
	return fmt.Sprintf("{permissions=%s, create/approve PRs=%t}", p.DefaultWorkflowPermissions, p.AllowCreateAndApprovePullRequests)
}
