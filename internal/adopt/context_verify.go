package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// contextVerifyTimeout bounds the verification after the chain (HISS-02), as the CLI bounds
// compile-context --verify (cmd/standardsctl/compile_context.go).
const contextVerifyTimeout = 2 * time.Minute

// maxVerifyFailures bounds opened verification failures (HISS-02). Each verifier may join
// multiple target failures; whatever remains at the bound stays joined in the last entry.
const maxVerifyFailures = 64

// maxVerifyDepth bounds single-wrapper inspection before a joined verification failure.
const maxVerifyDepth = 8

// verifyRejection leads every error the verification after the chain records. The verification
// reads the whole repository, not only what the run wrote: a file the repository placed in a
// persona directory, or an ignore rule a declined git-ignore step left to the operator, fails it
// too, so the error never says adoption wrote what it rejects.
const verifyRejection = "compile-context --verify rejects the repository's agent context after adoption"

// verifyAgentContext runs compile-context --verify (compiler.VerifyCompiledContext) over the
// repository once the chain has written it, so adoption never reports success on an agent context
// the next compile-context --verify and audit reject: a forced run once exited 0 with stale plugin
// persona copies (#359). Each rejection is an error on the report and on the step that owns what
// it rejects (verifyOwner), so the run is incomplete and the CLI exits non-zero. A caveman
// finding on text adoption keeps as written is a warning instead, as the agent-harness step
// already treats AGENTS.md (compiler.ErrContextProse, warned there, not repeated): a persona or
// skill the repository wrote (compiler.ErrAgentTextProse) and a nested AGENTS.md
// (compiler.ErrNestedContextProse) are warned here. A dry run wrote nothing
// and verifies nothing, and neither does a run that declines agent-harness or agent-definitions:
// adoption then leaves part of the agent context to the repository.
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
	for _, failure := range verifyFailures(err) {
		step := s.report.stepNamed(verifyOwner(failure))
		switch {
		case errors.Is(failure, compiler.ErrContextProse):
		case errors.Is(failure, compiler.ErrAgentTextProse), errors.Is(failure, compiler.ErrNestedContextProse):
			s.report.addStepWarning(step, fmt.Sprintf("%v; adoption keeps the repository's text as written", failure))
		default:
			s.report.addStepError(step, fmt.Sprintf("%s: %v", verifyRejection, failure))
		}
	}
}

// verifyOwner names the step that writes what failure rejects, so the step outcomes and the
// pillars they feed point at it: the evidence ignore rule is the git-ignore step's
// (compiler.ErrEvidenceNotIgnored), a persona or plugin skill copy (compiler.ErrAgentSurface) and
// the canonical persona and skill text (compiler.ErrAgentTextProse) are the agent-definitions
// step's, and everything else, AGENTS.md with its register block and the vendor files, is the
// agent-harness step's.
func verifyOwner(failure error) string {
	switch {
	case errors.Is(failure, compiler.ErrEvidenceNotIgnored):
		return "git-ignore"
	case errors.Is(failure, compiler.ErrAgentSurface), errors.Is(failure, compiler.ErrAgentTextProse):
		return "agent-definitions"
	default:
		return "agent-harness"
	}
}

// verifyFailures lists the separate failures err joins (errors.Join, or fmt.Errorf with several
// %w verbs), in order. Joins beneath up to maxVerifyDepth single wrappers are opened with each
// wrapper's context and surface ownership retained on each part. A wrapped single cause stays whole.
// At most maxVerifyFailures errors are opened; the unopened rest stays joined in the last entry.
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
			if parts := wrappedVerifyFailures(next); parts != nil {
				pending = append(parts, pending...)
			} else {
				failures = append(failures, next)
			}
		}
	}
	if rest := errors.Join(pending...); rest != nil {
		failures = append(failures, rest)
	}
	return failures
}

// wrappedVerifyFailures opens the first join beneath bounded single wrappers, or leaves a
// single cause intact. Excess children stay joined so expansion never discards a failure.
func wrappedVerifyFailures(err error) []error {
	next := err
	surface := false
	for depth := 0; next != nil && depth <= maxVerifyDepth; depth++ {
		if joined, ok := next.(interface{ Unwrap() []error }); ok {
			return prefixVerifyFailures(err, next, joined.Unwrap(), surface)
		}
		if marker, ok := next.(interface{ Is(error) bool }); ok {
			surface = surface || marker.Is(compiler.ErrAgentSurface)
		}
		next = errors.Unwrap(next)
	}
	return nil
}

// prefixVerifyFailures retains the wrapper text around a join on each bounded part. Wrappers
// that do not include their cause's text stay whole rather than losing diagnostic context.
func prefixVerifyFailures(original, joined error, causes []error, surface bool) []error {
	prefix, suffix, found := strings.Cut(original.Error(), joined.Error())
	if !found {
		return nil
	}
	parts := slices.Clone(causes)
	if len(parts) > maxVerifyFailures {
		parts = append(parts[:maxVerifyFailures-1], errors.Join(parts[maxVerifyFailures-1:]...))
	}
	for i := 0; i < len(parts) && i < maxVerifyFailures; i++ {
		parts[i] = verifyFailurePart{prefix: prefix, suffix: suffix, cause: parts[i], surface: surface}
	}
	return parts
}

// verifyFailurePart retains wrapper text and surface ownership while exposing only one cause.
// Its original join is never exposed through Unwrap or Is, so sibling lint cannot mask drift.
type verifyFailurePart struct {
	prefix, suffix string
	cause          error
	surface        bool
}

// Error substitutes the selected cause for the join, keeping the surrounding file and step.
func (e verifyFailurePart) Error() string {
	return e.prefix + e.cause.Error() + e.suffix
}

// Unwrap exposes the selected cause alone for classification and further bounded expansion.
func (e verifyFailurePart) Unwrap() error { return e.cause }

// Is retains ErrAgentSurface from projection wrappers without visiting joined siblings.
func (e verifyFailurePart) Is(target error) bool {
	return e.surface && target == compiler.ErrAgentSurface
}
