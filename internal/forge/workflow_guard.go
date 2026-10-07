package forge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// canonicalRepositoryVariable is the repository variable that names the canonical
	// repository. Forks do not inherit variables, so an unset value falls back to the
	// literal identity in the guard.
	canonicalRepositoryVariable = "PRAETOR_CANONICAL_REPOSITORY"
	manifestFileName            = ".standards.yaml"
)

// repositoryGuard is the one job condition that keeps an engine-only job inside the
// canonical repository. identity is "<owner>/<name>" from the repository manifest.
func repositoryGuard(identity string) string {
	return "github.repository == (vars." + canonicalRepositoryVariable + " || '" + identity + "')"
}

// guardHoldsInRepository reports whether a job condition is always true inside the
// repository named identity: a disjunction that contains the repository guard for exactly
// that identity. Such a job still reports on every pull request there, so it stays a
// required check context. Any conjunction or negation is treated as conditional, because
// its value is not knowable from the file.
func guardHoldsInRepository(condition, identity string) bool {
	return identity != "" && disjunctionContains(condition, repositoryGuard(identity))
}

// pullRequestRunTerm is the job condition term that holds on every pull request run: the one a
// scheduled leg's guard is joined to (security.yml), so the push and pull request legs run in
// every repository while the schedule leg stays in the canonical one.
const pullRequestRunTerm = "github.event_name != 'schedule'"

// holdsOnEveryPullRequestRun reports whether a job condition is a disjunction containing
// pullRequestRunTerm, and so holds on every pull request run whatever its other terms are.
func holdsOnEveryPullRequestRun(condition string) bool {
	return disjunctionContains(condition, pullRequestRunTerm)
}

// renovateHeadBranch and renovateAuthor are the two facts that make a pull request a Renovate pull
// request: its head branch starts with renovate/, and the Renovate app's bot account opened it.
// github.event.pull_request.user.login is the account that opened the pull request, which a later
// push or re-run does not change, and no person's account can carry the [bot] suffix. A person's
// pull request from a branch named renovate/... therefore matches only the first fact.
const (
	renovateHeadBranch = "startsWith(github.head_ref, 'renovate/')"
	renovateAuthor     = "github.event.pull_request.user.login == 'renovate[bot]'"
)

// renovateBranchSkip is the job condition conjunct that skips a job on a Renovate pull request
// only: both facts must hold. github.head_ref and github.event.pull_request are set on pull_request
// and pull_request_target runs only, and a missing property evaluates to an empty string, so the
// term holds on every push, schedule and dispatch run. The engine's heavy pull request jobs lead
// their condition with it (ci.yml, portability.yml, security.yml, pages.yml): a Renovate pull
// request is never merged as opened, the update is taken over on a signed-off branch whose own pull
// request runs every job.
const renovateBranchSkip = "!(" + renovateHeadBranch + " && " + renovateAuthor + ")"

// draftSkip is the job condition that skips a job on a draft pull request only. On a push run
// github.event.pull_request is absent and evaluates to an empty string, which GitHub coerces to
// 0 and true to 1, so the term holds there; on a pull request run it holds unless the pull
// request is a draft. The hosted gates adoption writes carry it (tools/apicompat,
// tools/markdownlint), so a draft no longer spends a run on them.
//
// GitHub reports the skipped job as successful on the draft's head commit, and refuses to merge
// a draft. The job stays required only where its workflow also runs on ready_for_review
// (rerunsWhenReady): marking the draft ready then runs the job on that head commit, and its
// result, not the skip, is the check the ruleset reads. Without that type the skip would stand
// as the head commit's passing check once the draft is ready, so the job is judged conditional.
const draftSkip = "github.event.pull_request.draft != true"

// readyForReviewType is the pull_request activity type GitHub sends when a draft is marked ready.
const readyForReviewType = "ready_for_review"

// withoutDraftSkip returns "" for a condition that is draftSkip alone, bare or as one ${{ }}
// expression, when rerunsOnReady says the workflow runs again once a draft is marked ready, and
// condition unchanged otherwise. Only the exact term counts: a negated, joined or reworded draft
// test stays conditional.
func withoutDraftSkip(condition string, rerunsOnReady bool) string {
	if rerunsOnReady && unwrapExpression(condition) == draftSkip {
		return ""
	}
	return condition
}

// unwrapExpression returns condition trimmed, with one ${{ }} wrapper around the whole of it
// removed, so a bare condition and the same condition as one expression read alike. An
// unclosed wrapper is returned as it stands.
func unwrapExpression(condition string) string {
	expression := strings.TrimSpace(condition)
	inner, wrapped := strings.CutPrefix(expression, "${{")
	if !wrapped {
		return expression
	}
	body, closed := strings.CutSuffix(inner, "}}")
	if !closed {
		return expression
	}
	return strings.TrimSpace(body)
}

// withoutRenovateBranchSkip returns condition with a leading renovateBranchSkip conjunct removed,
// so the rest is judged as if the job had no such skip. Only the exact two-fact term counts: a
// looser skip, such as the head branch test alone, stays in place and makes the job conditional.
// The result is "" for the term alone, the inside of one parenthesised group that encloses the
// whole rest, or a rest without a top-level disjunction (topLevelDisjunction). Any other shape,
// such as a rest whose top-level || would bind around the conjunction or whose parentheses do not
// pair, returns condition unchanged, and its && then makes the job conditional.
func withoutRenovateBranchSkip(condition string) string {
	condition = strings.TrimSpace(condition)
	if condition == renovateBranchSkip {
		return ""
	}
	rest, found := strings.CutPrefix(condition, renovateBranchSkip+" && ")
	if !found {
		return condition
	}
	rest = strings.TrimSpace(rest)
	if inner, enclosed := enclosedGroup(rest); enclosed {
		return inner
	}
	if strings.Count(rest, "(") != strings.Count(rest, ")") || topLevelDisjunction(rest) {
		return condition
	}
	return rest
}

// topLevelDisjunction reports whether expression holds a || outside every parenthesised group,
// such as "a || b" but not "a == (b || c)".
func topLevelDisjunction(expression string) bool {
	for i := 0; i < len(expression); i++ {
		if expression[i] == '(' {
			i += len(util.EnclosedParens(expression, i)) + 1
			continue
		}
		if strings.HasPrefix(expression[i:], "||") {
			return true
		}
	}
	return false
}

// enclosedGroup returns the inside of expression when one pair of parentheses encloses all of it,
// so that "(a || b)" yields "a || b" while "(a) || (b)" is no group.
func enclosedGroup(expression string) (string, bool) {
	inner := util.EnclosedParens(expression, 0)
	if !strings.HasPrefix(expression, "(") || len(inner)+2 != len(expression) {
		return "", false
	}
	return strings.TrimSpace(inner), true
}

// disjunctionContains reports whether condition is a disjunction holding term, so that the
// condition is true wherever term is. Any conjunction or negated group is refused, because
// its value is not knowable from the file.
func disjunctionContains(condition, term string) bool {
	condition = strings.TrimSpace(condition)
	if strings.Contains(condition, "&&") || strings.Contains(condition, "!(") {
		return false
	}
	return strings.Contains(condition, term)
}

// guardIdentity resolves the repository identity only when a workflow carries a repository
// guard, so a repository without guards is read exactly as before.
func guardIdentity(files []workflowFile, repoPath string) (string, error) {
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if strings.Contains(string(files[i].Data), "vars."+canonicalRepositoryVariable) {
			return manifestIdentity(repoPath)
		}
	}
	return "", nil
}

// manifestIdentity returns "<owner>/<name>" from the repository manifest, or "" when the
// repository has no manifest or the manifest names no repository. Without an identity a
// guarded job stays conditional, which is the behaviour before guards existed.
//
// repository.source, when present, is preferred over owner/name. An operational fork's owner
// overlay (internal/operationalsync) rewrites owner/name to the fork's own identity but never
// touches the checked-in workflow files, so their repository guard literals keep naming the
// public source. Resolving owner/name there would make guardHoldsInRepository compare the
// fork's identity against a literal that still names the source and read every guarded job as
// conditional, which is exactly issue #255: the fork's own guard and ruleset tests fail against
// its own checkout. config.LoadManifest has already rejected a malformed source.
func manifestIdentity(repoPath string) (string, error) {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repository identity: %w", err)
	}
	if manifest.Repository.Source != "" {
		return manifest.Repository.Source, nil
	}
	if manifest.Repository.Owner == "" || manifest.Repository.Name == "" {
		return "", nil
	}
	return manifest.Repository.Owner + "/" + manifest.Repository.Name, nil
}
