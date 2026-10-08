package adopt

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
)

// Adoption's record of the agent surfaces the agent-definitions step projects from the canonical
// personas and skills: the persona copy in every persona directory agent_clients selects
// (.claude/agents/<name>.md and the others), the copy of every register skill the repository
// carries in every skill directory it selects (.claude/skills/<name>/SKILL.md, #235) and, when the
// repository ships the plugin (compiler.PluginManifestRel), the plugin persona and skill copies. The step writes them through
// compiler.CompileAgentSurfaces, the writer compile-context uses, so adoption projects every copy
// compile-context --verify checks; it used to write only the client copies, and a forced run left
// the plugin copies stale (#359). compile-context --verify demands their exact bytes, so every
// real run rewrites them; what differs is how the report lists an existing one, by the rule the
// vendor context files follow (recordProjections): a copy that already holds its text, or the
// copy of its canonical source as the run found it, is synchronized; any other existing copy
// holds a hand edit, so it is replaced with a backup. A re-run no longer lists every existing
// copy as created.

// maxProjectionTargets bounds the loops over one compiled projection set (HISS-02) and is the
// most copies one run projects: compiler.MaxAgentFiles canonical personas, each copied into the
// persona directory of every selected agent client and the plugin, plus the plugin skill copies,
// with room for more clients than agentcontext registers today. The vendor context files are far
// fewer.
const maxProjectionTargets = compiler.MaxAgentFiles * 16

// agentSurfaceKind names one kind of copy the agent-definitions step projects: its canonical
// source directory and the report details of a created or replaced and a synchronized copy.
type agentSurfaceKind struct {
	source, projected, synchronized string
}

// agentSurfaceKindOf returns the kind of the copy at rel, a slash path compiler.PlanAgentSurfaces
// listed: a plugin skill copy, a plugin persona copy, the skill copy of an agent client (a
// SKILL.md, which no persona copy is), or the persona copy of an agent client.
func agentSurfaceKindOf(rel string) agentSurfaceKind {
	switch {
	case strings.HasPrefix(rel, compiler.PluginSkillsRel+"/"):
		return agentSurfaceKind{source: compiler.CanonicalSkillsRel,
			projected:    "Projected canonical skill to the plugin copy",
			synchronized: "Synchronized plugin copy of the canonical skill"}
	case path.Base(rel) == compiler.SkillEntryName:
		return agentSurfaceKind{source: compiler.CanonicalSkillsRel,
			projected:    "Projected canonical skill to the skill directory its agent client reads",
			synchronized: "Synchronized client copy of the canonical skill"}
	case strings.HasPrefix(rel, compiler.PluginAgentsRel+"/"):
		return agentSurfaceKind{source: compiler.CanonicalAgentsRel,
			projected:    "Projected canonical agent definition to the plugin copy",
			synchronized: "Synchronized plugin copy of the canonical agent definition"}
	default:
		return agentSurfaceKind{source: compiler.CanonicalAgentsRel,
			projected:    "Projected canonical agent definition to vendor target",
			synchronized: "Synchronized vendor copy of the canonical agent definition"}
	}
}

// agentSurfaceLabels labels the copies the agent-definitions step projects, by kind.
var agentSurfaceLabels = projectionLabels{
	compiled: func(file compiler.TargetFile) string {
		return agentSurfaceKindOf(file.RelativePath).projected
	},
	synchronized: func(file compiler.TargetFile) string {
		return agentSurfaceKindOf(file.RelativePath).synchronized
	},
}

// plannedAgentSurfaces returns the copies compiler.CompileAgentSurfaces writes for the repository
// as it is now, as the compiled files the projection record reads. pending holds the canonical
// personas and skills a dry run would have written by then (pendingSources); empty reads disk.
func plannedAgentSurfaces(ctx context.Context, repoPath string, pending compiler.PendingSources) ([]compiler.TargetFile, error) {
	planned, err := compiler.PlanAgentSurfacesOver(ctx, repoPath, pending)
	if err != nil {
		return nil, fmt.Errorf("plan agent definitions: %w", err)
	}
	if len(planned) > maxProjectionTargets {
		return nil, fmt.Errorf("agent definitions would project %d copies, above the %d a run reports", len(planned), maxProjectionTargets)
	}
	return planned, nil
}

// priorAgentSurfaces returns the digest of every copy the canonical personas and skills render as
// the run found them, before the agent-definitions step refreshes the personas: the copies an
// unedited earlier run left, which a write over them synchronizes rather than replaces.
func priorAgentSurfaces(ctx context.Context, repoPath string) (priorVendorProjections, error) {
	files, err := plannedAgentSurfaces(ctx, repoPath, compiler.PendingSources{})
	if err != nil {
		return nil, err
	}
	return agentSurfaceDigests(files), nil
}

// agentSurfaceDigests returns the digest of each planned copy, in the form a write over the copy
// compares against (priorVendorProjections).
func agentSurfaceDigests(files []compiler.TargetFile) priorVendorProjections {
	prior := make(priorVendorProjections, len(files))
	for i := 0; i < len(files) && i < maxProjectionTargets; i++ {
		prior.record(files[i].RelativePath, files[i].Content, agentSurfaceKindOf(files[i].RelativePath).source)
	}
	return prior
}

// projectAgentSurfaces writes every copy (compiler.CompileAgentSurfaces) and records each one
// against prior (priorAgentSurfaces): created, synchronized, or replaced with a backup kept
// before the write (replaceExistingAll). A dry run writes nothing and records the same entries
// for the copies of the canonical personas and skills its real run leaves (pendingPersonas,
// pendingSkills), so the preview names every copy the run projects and every hand edit it
// replaces (#366).
func projectAgentSurfaces(ctx context.Context, s *adoptSession, prior priorVendorProjections) error {
	pending := compiler.PendingSources{Personas: s.pendingPersonas(), Skills: s.pendingSkills(), Licenses: s.pendingLicenses()}
	files, err := plannedAgentSurfaces(ctx, s.repoPath, pending)
	if err != nil {
		return err
	}
	targets, err := observeVendorTargets(ctx, s.repoPath, files)
	if err != nil {
		return err
	}
	publish := func(ctx context.Context) error {
		if _, err := compiler.CompileAgentSurfaces(ctx, io.Discard, s.repoPath); err != nil {
			return fmt.Errorf("compile agent definitions: %w", err)
		}
		return nil
	}
	if err := s.replaceExistingAll(ctx, projectionReplacements(targets, prior, agentSurfaceLabels), publish); err != nil {
		return err
	}
	recordProjections(s.report, targets, prior, agentSurfaceLabels)
	return nil
}

// pendingPersonas returns, in a dry run, the canonical personas the agent-definitions step
// planned to write or refresh (planDryRunWrite), keyed by file name, which a real run has on
// disk by the time it projects; a real run, and a dry run that writes no persona, get none.
func (s *adoptSession) pendingPersonas() map[string][]byte {
	if !s.opts.DryRun {
		return nil
	}
	personas := generatedPersonas()
	pending := make(map[string][]byte, len(personas))
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		if content := s.dryRunWrites[personas[i].rel]; content != nil {
			pending[path.Base(personas[i].rel)] = content
		}
	}
	return pending
}

// preflightPersonaBackupRoot refuses, on a run without --force, a backup root checkBackupRoot
// refuses when the agent-definitions step may replace a hand-edited copy: an existing copy that
// is not the copy of its canonical source as the run found it. That step backs such a copy up
// mid-run, where a refused root would fail after the earlier steps had written. The check is a
// superset of the step's own condition: it reads the persona directories the manifest selects
// before a first adoption writes one. Under --force preflightForceBackupRoot checks the root
// whatever the steps.
func preflightPersonaBackupRoot(ctx context.Context, s *adoptSession) error {
	if s.opts.Force {
		return nil
	}
	files, err := plannedAgentSurfaces(ctx, s.repoPath, compiler.PendingSources{})
	if err != nil {
		return err
	}
	targets, err := observeVendorTargets(ctx, s.repoPath, files)
	if err != nil {
		return err
	}
	prior := agentSurfaceDigests(files)
	for i := 0; i < len(targets) && i < maxProjectionTargets; i++ {
		if targets[i].replacesEdit(prior) {
			return checkBackupRoot(ctx, s.repoPath)
		}
	}
	return nil
}
