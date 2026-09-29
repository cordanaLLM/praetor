package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// contextVerifyTimeout bounds the verification after the chain (HISS-02), as the CLI bounds
// compile-context --verify (cmd/standardsctl/compile_context.go).
const contextVerifyTimeout = 2 * time.Minute

// maxVerifyFailures bounds the failures one verification lists (HISS-02). VerifyCompiledContext
// joins at most eight checks (register block, evidence ignore rule, vendor files, three lints,
// persona and plugin skill projections), so the bound is never reached by a real run.
const maxVerifyFailures = 64

// contextVerifiedStep is the step whose outcome carries the verification's findings: the one the
// AI Context Sync pillar reads, so that pillar never claims a context the verification rejected.
const contextVerifiedStep = "agent-harness"

// verifyAgentContext runs compile-context --verify (compiler.VerifyCompiledContext) over the
// repository once the chain has written it, so adoption never reports success on an agent context
// the next compile-context --verify and audit reject: a forced run once exited 0 with stale plugin
// persona copies (#359). Each rejection is an error on the report and on the agent-harness step,
// so the run is incomplete and the CLI exits non-zero. A caveman finding on text adoption keeps as
// written is a warning instead, as the agent-harness step already treats AGENTS.md
// (compiler.ErrContextProse, warned there, not repeated): a persona or skill the repository wrote
// (compiler.ErrAgentTextProse) is warned here. A dry run wrote nothing and verifies nothing, and
// neither does a run that declines agent-harness or agent-definitions: adoption then leaves part
// of the agent context to the repository.
func verifyAgentContext(ctx context.Context, s *adoptSession, declined map[string]bool) {
	if s.opts.DryRun || declined["agent-harness"] || declined["agent-definitions"] {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, contextVerifyTimeout)
	defer cancel()
	source := filepath.Join(s.repoPath, agentsFile)
	err := compiler.VerifyCompiledContext(ctx, io.Discard, compiler.NewTranspiler(), source, s.repoPath)
	if err == nil {
		return
	}
	var warnings, errs []string
	for _, failure := range verifyFailures(err) {
		switch {
		case errors.Is(failure, compiler.ErrContextProse):
		case errors.Is(failure, compiler.ErrAgentTextProse):
			warnings = append(warnings, fmt.Sprintf("%v; adoption keeps the repository's text as written", failure))
		default:
			errs = append(errs, fmt.Sprintf("compile-context --verify rejects the agent context adoption wrote: %v", failure))
		}
	}
	s.report.attachToStep(contextVerifiedStep, warnings, errs)
}

// verifyFailures lists the separate failures err joins (errors.Join, or fmt.Errorf with several
// %w verbs), in order: every error that joins others is opened, and a failure wrapped once stays
// whole, with its context. At most maxVerifyFailures errors are opened; whatever is left then
// stays joined in the last entry, so nothing is dropped.
func verifyFailures(err error) []error {
	pending := []error{err}
	var failures []error
	for i := 0; len(pending) > 0 && i < maxVerifyFailures; i++ {
		next := pending[0]
		pending = pending[1:]
		joined, ok := next.(interface{ Unwrap() []error })
		switch {
		case next == nil:
		case ok:
			pending = append(slices.Clone(joined.Unwrap()), pending...)
		default:
			failures = append(failures, next)
		}
	}
	if rest := errors.Join(pending...); rest != nil {
		failures = append(failures, rest)
	}
	return failures
}
