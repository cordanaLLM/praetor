package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// contextVerifyTimeout bounds the verification after the chain (HISS-02), as the CLI bounds
// compile-context --verify (cmd/standardsctl/compile_context.go).
const contextVerifyTimeout = 2 * time.Minute

// maxVerifyFailures bounds verification expansion steps (HISS-02), including joins.
// Whatever remains unopened at the bound stays joined and explicitly marked in the last entry.
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
	recordContextVerification(s.report, err)
}

// recordContextVerification classifies opened causes only; an unopened aggregate always stops
// its step, even when a lint sentinel is reachable through its diagnostic chain.
func recordContextVerification(report *AdoptReport, err error) {
	for _, failure := range verifyFailures(err) {
		step := report.stepNamed(failure.owner())
		switch {
		case failure.bound != "":
			report.addStepError(step, fmt.Sprintf("%s: %v", verifyRejection, failure))
		case errors.Is(failure, compiler.ErrContextProse):
		case errors.Is(failure, compiler.ErrAgentTextProse), errors.Is(failure, compiler.ErrNestedContextProse):
			report.addStepWarning(step, fmt.Sprintf("%v; adoption keeps the repository's text as written", failure))
		default:
			report.addStepError(step, fmt.Sprintf("%s: %v", verifyRejection, failure))
		}
	}
}

// verifyOwner names the step that writes what failure rejects, so the step outcomes and the
// pillars they feed point at it: the evidence ignore rule is the git-ignore step's
// (compiler.ErrEvidenceNotIgnored), a persona or plugin skill copy (compiler.ErrAgentSurface) and
// the canonical persona and skill text (compiler.ErrAgentTextProse) are the agent-definitions
// step's, and everything else, AGENTS.md with its register block and the vendor files, is the
// agent-harness step's. Only opened causes reach this sentinel classifier (verifyFailure.owner).
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
// At most maxVerifyFailures expansion steps run; unopened aggregates carry an explicit bound.
func verifyFailures(err error) []verifyFailure {
	pending := []verifyFailure{{cause: err}}
	var failures []verifyFailure
	for i := 0; len(pending) > 0 && i < maxVerifyFailures; i++ {
		next := pending[0]
		pending = pending[1:]
		switch {
		case next.cause == nil:
		case next.bound != "":
			failures = append(failures, next)
		default:
			if parts, leaf := wrappedVerifyFailures(next); parts != nil {
				pending = append(parts, pending...)
			} else {
				failures = append(failures, leaf)
			}
		}
	}
	if len(pending) > 0 {
		failures = append(failures, pendingVerifyFailures(pending))
	}
	return failures
}

// wrappedVerifyFailures opens the first join beneath bounded single wrappers, or leaves a
// single cause intact. Excess children stay joined so expansion never discards a failure.
func wrappedVerifyFailures(failure verifyFailure) ([]verifyFailure, verifyFailure) {
	next := failure.cause
	surface := failure.surface
	for depth := 0; next != nil && depth <= maxVerifyDepth; depth++ {
		if joined, ok := next.(interface{ Unwrap() []error }); ok {
			return prefixVerifyFailures(failure.cause, next, joined.Unwrap(), surface), failure
		}
		if marker, ok := next.(interface{ Is(error) bool }); ok {
			surface = surface || marker.Is(compiler.ErrAgentSurface)
		}
		next = errors.Unwrap(next)
	}
	if next != nil {
		failure.bound = "depth"
		failure.surface = surface
	}
	return nil, failure
}

// prefixVerifyFailures retains the wrapper text around a join on each bounded part. Wrappers
// that do not include their cause's text stay whole and marked rather than losing context.
func prefixVerifyFailures(original, joined error, causes []error, surface bool) []verifyFailure {
	prefix, suffix, found := strings.Cut(original.Error(), joined.Error())
	if !found {
		return []verifyFailure{{cause: original, bound: "wrapper context", count: len(causes), surface: surface}}
	}
	var parts []verifyFailure
	for i := 0; i < len(causes) && i < maxVerifyFailures; i++ {
		part := verifyFailure{cause: causes[i], surface: surface}
		if i == maxVerifyFailures-1 && len(causes) > maxVerifyFailures {
			part.cause = errors.Join(causes[i:]...)
			part.bound, part.count = "expansion", len(causes)-i
		}
		part.cause = verifyFailurePart{prefix: prefix, suffix: suffix, cause: part.cause, surface: surface}
		parts = append(parts, part)
	}
	return parts
}

// verifyFailure distinguishes an opened cause from an aggregate left unopened at a bound.
// count records immediate pending failures; their nested failure count can remain unknown.
type verifyFailure struct {
	cause   error
	bound   string
	count   int
	surface bool
}

// Error keeps all wrapper diagnostics and explains why even lint-only overflow stops adoption.
func (e verifyFailure) Error() string {
	if e.bound == "" {
		return e.cause.Error()
	}
	count := "failure count unknown"
	if e.count > 0 {
		count = fmt.Sprintf("%d pending failures; nested failure count unknown", e.count)
	}
	return fmt.Sprintf("verification %s bound reached (%s): %v", e.bound, count, e.cause)
}

// Unwrap retains diagnostic identities; callers must check bound before classifying by sentinel.
func (e verifyFailure) Unwrap() error { return e.cause }

// owner classifies opened causes only. Explicit surface ancestry owns an unopened remainder;
// mixed or unknown ownership conservatively selects agent-harness without sentinel inspection.
func (e verifyFailure) owner() string {
	if e.bound == "" {
		return verifyOwner(e.cause)
	}
	if e.surface {
		return "agent-definitions"
	}
	return "agent-harness"
}

// pendingVerifyFailures keeps unvisited diagnostics without inspecting their sentinels.
// Each of at most maxVerifyFailures expansions adds at most maxVerifyFailures entries,
// so their product bounds the entire frontier without dropping late failures.
func pendingVerifyFailures(pending []verifyFailure) verifyFailure {
	causes := make([]error, 0, len(pending))
	surface := true
	for i := 0; i < len(pending) && i < maxVerifyFailures*maxVerifyFailures; i++ {
		causes = append(causes, pending[i])
		surface = surface && pending[i].surface
	}
	return verifyFailure{cause: errors.Join(causes...), bound: "expansion", count: len(pending), surface: surface}
}

// verifyFailurePart retains wrapper text and surface ownership while exposing only one cause.
// An opened part exposes only its selected cause. A grouped tail carries verifyFailure's
// bound metadata, so its sibling lint cannot mask drift.
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
