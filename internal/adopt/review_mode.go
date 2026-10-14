package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// githubHost is the forge host whose collaborator listing adoption can read.
const githubHost = "github.com"

// errNoForgeToken says no forge token was set for the collaborator count.
var errNoForgeToken = errors.New("no forge token in GITHUB_TOKEN or GH_TOKEN")

// CollaboratorCounter returns how many accounts can push to owner/name
// (AdoptOptions.Collaborators). The CLI and the MCP server pass GitHubWriteCollaborators; a nil
// counter leaves the review mode unexamined, so a library caller or a test never reaches the
// forge unasked.
type CollaboratorCounter func(ctx context.Context, owner, name string) (int, error)

// GitHubWriteCollaborators counts the collaborators of owner/name on GitHub with write access,
// with the token from GITHUB_TOKEN or GH_TOKEN, never the gh session, as sync --remote reads it:
// a logged-in workstation does not make adoption reach the forge unasked.
func GitHubWriteCollaborators(ctx context.Context, owner, name string) (int, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return 0, errNoForgeToken
	}
	driver := forge.NewGitHubDriver(token, "")
	driver.SetRepository(owner, name)
	return driver.WriteCollaborators(ctx)
}

// soloMaintainer reports whether the repository has exactly one collaborator with write access,
// and the report note that says what adoption concluded, resolved once per run (#1075). One
// writer cannot approve their own pull request, so any approval count the archetype asks for
// locks the owner out once sync --remote applies the ruleset. Without a counter the question is
// not asked and there is no note; without a forge token, without a GitHub origin, or when the
// forge cannot answer, the count is unknown and the note warns that approvals may exceed
// approvers.
func (s *adoptSession) soloMaintainer(ctx context.Context) (bool, string) {
	if s.review.resolved {
		return s.review.solo, s.review.note
	}
	s.review.resolved = true
	if s.collaborators == nil {
		return false, ""
	}
	if !s.identity.resolved() {
		s.review.note = "approvals may exceed approvers: the repository identity is unresolved, so adoption cannot count its collaborators"
		return false, s.review.note
	}
	if s.identity.host != githubHost {
		s.review.note = "approvals may exceed approvers: adoption counts collaborators on a GitHub origin only"
		return false, s.review.note
	}
	writers, err := s.collaborators(ctx, s.identity.owner, s.identity.name)
	switch {
	case err != nil:
		s.review.note = fmt.Sprintf("approvals may exceed approvers: adoption could not count the collaborators with write access (%v)", err)
	case writers == 1:
		s.review.solo = true
		s.review.note = fmt.Sprintf("overrides.branch_protection.review_mode: %s, because %s has one collaborator with write access, "+
			"who cannot approve their own pull request", config.BranchReviewModeSingleMaintainer, s.identity.coordinate())
	}
	return s.review.solo, s.review.note
}

// soloOverrides returns the manifest overrides a repository with one writer gets: the
// single-maintainer review mode, the one relaxation overrides may carry (config.ApplyOverrides).
func soloOverrides() config.Overrides {
	return config.Overrides{BranchProtection: &config.BranchProtectionPolicy{ReviewMode: config.BranchReviewModeSingleMaintainer}}
}

// warnUnsatisfiableReview warns about an existing manifest of a one-writer repository that
// declares no single-maintainer review mode: adoption never rewrites a manifest, so it names the
// declaration to add.
func (s *adoptSession) warnUnsatisfiableReview(ctx context.Context, declared *config.Manifest) {
	solo, note := s.soloMaintainer(ctx)
	switch {
	case solo && (declared.Overrides.BranchProtection == nil ||
		declared.Overrides.BranchProtection.ReviewMode != config.BranchReviewModeSingleMaintainer):
		s.report.addWarning("%s declares no %s review mode, but %s has one collaborator with write access: its ruleset asks "+
			"for approvals nobody can give; declare overrides.branch_protection.review_mode: %s", manifestFile,
			config.BranchReviewModeSingleMaintainer, s.identity.coordinate(), config.BranchReviewModeSingleMaintainer)
	case !solo && note != "":
		s.report.addWarning("%s", note)
	}
}
