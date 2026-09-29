package adopt

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

// lefthookProbeTimeout bounds the `lefthook version` probe (HISS-02).
const lefthookProbeTimeout = 10 * time.Second

// hookActivation is what the git-hooks step leaves active, as rule 5 of the harness states it.
type hookActivation uint8

const (
	// hooksNone: git-hooks declined, or lefthook.yml is not praetor's rendering.
	hooksNone hookActivation = iota
	// hooksInactive: praetor's lefthook.yml, but lefthook will not install its hooks here.
	hooksInactive
	// hooksLefthook: praetor's lefthook.yml with its hooks installed by lefthook.
	hooksLefthook
)

// generatedPipelines is what this run generates for the harness table to credit. The harness
// step runs before the makefile and git-hooks steps, so it decides from what those steps will
// do, not from what they did:
//
//   - verify-all: the makefile step runs and the plan is not preserved. A preserved custom
//     verify-all is repository-owned, and adoption neither reads nor runs it.
//   - lefthook: plannedHookActivation predicts praetor's hooks installed by lefthook.
//
// Crediting either pipeline without this check was the BUG-804 review finding: a preserved
// verify-all still claimed `compile-context --verify`, and a foreign lefthook.yml, or one
// lefthook never installed, still claimed praetor's hook jobs.
func (s *adoptSession) generatedPipelines(ctx context.Context) (hisscatalog.Pipeline, hookActivation, error) {
	var generated hisscatalog.Pipeline
	makefileDeclined, err := ArtifactDeclined(s.declined, "makefile")
	if err != nil {
		return 0, hooksNone, fmt.Errorf("resolve adoption.decline for the harness: %w", err)
	}
	if !makefileDeclined && s.verification.Status != verificationPreserved {
		generated |= hisscatalog.PipelineVerifyAll
	}
	hooks, err := s.plannedHookActivation(ctx)
	if err != nil {
		return 0, hooksNone, err
	}
	if hooks == hooksLefthook {
		generated |= hisscatalog.PipelineLefthook
	}
	return generated, hooks, nil
}

// plannedHookActivation predicts what reconcileGitHooks leaves active. Its lefthook.yml runs
// only once `lefthook install` wires it into git: activateGitHooks runs that install and, when
// it fails, writes the fallback pre-commit hook, which runs compile-context --verify and audit
// alone (installFallbackHook). Activation skipped by option installs nothing. Either way the
// lefthook stages do not run, so they are credited only when lefthook itself runs.
func (s *adoptSession) plannedHookActivation(ctx context.Context) (hookActivation, error) {
	declined, err := ArtifactDeclined(s.declined, "git-hooks")
	if err != nil {
		return hooksNone, fmt.Errorf("resolve adoption.decline for the harness: %w", err)
	}
	if declined {
		return hooksNone, nil
	}
	owned, err := s.plannedLefthookOwned()
	if err != nil || !owned {
		return hooksNone, err
	}
	if s.opts.SkipHookActivation || !lefthookRunnable(ctx) {
		return hooksInactive, nil
	}
	return hooksLefthook, nil
}

// lefthookRunnable reports whether `lefthook version` runs, the same binary resolution
// activateGitHooks's `lefthook install` goes through (util.RunCommand). A missing binary, or
// one that fails, is no lefthook pipeline. The probe runs in the temporary directory, so it
// reads no repository configuration.
func lefthookRunnable(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, lefthookProbeTimeout)
	defer cancel()
	_, err := util.RunCommand(probeCtx, os.TempDir(), "lefthook", "version")
	return err == nil
}

// plannedLefthookOwned predicts whether reconcileGitHooks leaves praetor's rendering in
// lefthook.yml: it writes an absent file, migrates an earlier rendering, keeps a current one,
// and rewrites a line-ending checkout of a current one only under --force. Every other
// configuration is kept, --force included (classifyLefthookConfig), and is not praetor's. When
// the prediction cannot tell, the harness under-claims rather than credit a gate the repository
// may not get. TestGeneratedPipelinesPredictGitHooks replays each case against the step.
func (s *adoptSession) plannedLefthookOwned() (bool, error) {
	existing, exists, err := s.readExistingLefthook()
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	languages := s.lefthookLanguages()
	identity := classifyLefthookConfig(existing, languages)
	if identity.reason != "" {
		return false, nil
	}
	match := matchCurrentLefthook(existing, languages)
	return identity.prior || match.exact || (match.found && s.opts.Force), nil
}
