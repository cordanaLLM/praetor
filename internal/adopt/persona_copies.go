package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// Adoption's record of the persona copies (.claude/agents/<name>.md and the other persona
// directories agent_clients selects), which the agent-definitions step projects from the
// canonical personas under .agents/agents through compiler.CompileAgents. compile-context
// --verify demands their exact bytes, so every real run rewrites them; what differs is how
// the report lists an existing one, by the rule the vendor context files follow
// (recordProjections): a copy that already holds its text, or the copy of the canonical
// persona as the run found it, is synchronized; any other existing copy holds a hand edit, so
// it is replaced with a backup. A re-run no longer lists every existing copy as created.

// maxProjectionTargets bounds the loops over one compiled projection set (HISS-02) and is the
// most persona copies one run projects: compiler.MaxAgentFiles canonical personas, each copied
// into the persona directory of every selected agent client, with room for more clients than
// agentcontext registers today. The vendor context files are far fewer.
const maxProjectionTargets = compiler.MaxAgentFiles * 16

// personaCopyLabels labels the persona copies the agent-definitions step projects.
var personaCopyLabels = projectionLabels{
	compiled: func(compiler.TargetFile) string {
		return "Projected canonical agent definition to vendor target"
	},
	synchronized: func(compiler.TargetFile) string {
		return "Synchronized vendor copy of the canonical agent definition"
	},
}

// planPersonaCopies returns the persona copies compiler.CompileAgents writes for the repository
// as it is now, as the compiled files the projection record reads.
func planPersonaCopies(ctx context.Context, repoPath string) ([]compiler.TargetFile, error) {
	planned, err := compiler.PlanAgents(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("plan agent definitions: %w", err)
	}
	if len(planned) > maxProjectionTargets {
		return nil, fmt.Errorf("agent definitions would project %d persona copies, above the %d a run reports", len(planned), maxProjectionTargets)
	}
	files := make([]compiler.TargetFile, 0, len(planned))
	for i := 0; i < len(planned) && i < maxProjectionTargets; i++ {
		files = append(files, compiler.TargetFile{RelativePath: planned[i].VendorTarget, Content: planned[i].Content})
	}
	return files, nil
}

// priorPersonaCopies returns the digest of every persona copy the canonical personas render as
// the run found them, before the agent-definitions step refreshes them: the copies an unedited
// earlier run left, which a write over them synchronizes rather than replaces.
func priorPersonaCopies(ctx context.Context, repoPath string) (priorVendorProjections, error) {
	files, err := planPersonaCopies(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return personaCopyDigests(files), nil
}

// personaCopyDigests returns the digest of each planned persona copy, in the form a write over
// the copy compares against (priorVendorProjections).
func personaCopyDigests(files []compiler.TargetFile) priorVendorProjections {
	prior := make(priorVendorProjections, len(files))
	for i := 0; i < len(files) && i < maxProjectionTargets; i++ {
		prior.record(files[i].RelativePath, files[i].Content, compiler.CanonicalAgentsRel)
	}
	return prior
}

// projectPersonaCopies writes the persona copies (compiler.CompileAgents) and records each one
// against prior (priorPersonaCopies): created, synchronized, or replaced with a backup kept
// before the write (replaceExistingAll).
func projectPersonaCopies(ctx context.Context, s *adoptSession, prior priorVendorProjections) error {
	files, err := planPersonaCopies(ctx, s.repoPath)
	if err != nil {
		return err
	}
	targets, err := observeVendorTargets(ctx, s.repoPath, files)
	if err != nil {
		return err
	}
	publish := func(ctx context.Context) error {
		if _, err := compiler.CompileAgents(ctx, s.repoPath); err != nil {
			return fmt.Errorf("compile agent definitions: %w", err)
		}
		return nil
	}
	if err := s.replaceExistingAll(ctx, projectionReplacements(targets, prior, personaCopyLabels), publish); err != nil {
		return err
	}
	recordProjections(s.report, targets, prior, personaCopyLabels)
	return nil
}

// preflightPersonaBackupRoot refuses, on a run without --force, a backup root checkBackupRoot
// refuses when the agent-definitions step may replace a hand-edited persona copy: an existing
// copy that is not the copy of its canonical persona as the run found it. That step backs such
// a copy up mid-run, where a refused root would fail after the earlier steps had written. The
// check is a superset of the step's own condition: it reads the persona directories the
// manifest selects before a first adoption writes one. Under --force preflightForceBackupRoot
// checks the root whatever the steps.
func preflightPersonaBackupRoot(ctx context.Context, s *adoptSession) error {
	if s.opts.Force {
		return nil
	}
	files, err := planPersonaCopies(ctx, s.repoPath)
	if err != nil {
		return err
	}
	targets, err := observeVendorTargets(ctx, s.repoPath, files)
	if err != nil {
		return err
	}
	prior := personaCopyDigests(files)
	for i := 0; i < len(targets) && i < maxProjectionTargets; i++ {
		if targets[i].replacesEdit(prior) {
			return checkBackupRoot(ctx, s.repoPath)
		}
	}
	return nil
}
