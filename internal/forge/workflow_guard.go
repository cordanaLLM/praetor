package forge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
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
