package adopt

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Adoption's record of the vendor context files (CLAUDE.md and the other compile-context
// projections of AGENTS.md). audit demands their exact bytes, so the agent-harness step always
// rewrites them, on a plain run too; what differs is how the report lists an existing one. A
// file that already holds its new projection, or the projection of AGENTS.md as the run found
// it or as HEAD holds it (compile-context output nobody edited, AGENTS.md edited since), is
// synchronized. Any other existing file holds a hand edit the rewrite drops, so it is replaced
// with a backup (replaceExistingAll).

// priorVendorProjections maps each vendor path to the digests (util.CanonicalTextDigest) of
// the projections of AGENTS.md as the run found it and as HEAD holds it, in the form
// util.LookupCanonicalText reads.
type priorVendorProjections map[string]map[string]string

// headAgentsRevision names AGENTS.md in the HEAD commit, relative to the repository directory
// git runs in, which is the adoption target.
const headAgentsRevision = "HEAD:./" + agentsFile

// vendorTarget is one vendor context file the agent-harness step writes, with the bytes it
// held before the write.
type vendorTarget struct {
	file   compiler.TargetFile
	before []byte
	exists bool
}

// priorVendorTexts compiles AGENTS.md as it is on disk and as HEAD holds it
// (committedAgentsText) for the vendor files clients selects and returns the digests of their
// projections. It runs before the agent-harness step rewrites AGENTS.md. The HEAD text covers
// an AGENTS.md edited after the last compile-context: its vendor files are still the
// projection of the committed text. An absent AGENTS.md, or one that does not compile (a
// foreign file adoption has not merged yet), yields none, so every existing vendor file that
// differs from the new projection and from the HEAD one counts as edited.
func priorVendorTexts(ctx context.Context, repoPath string, clients []string) (priorVendorProjections, error) {
	prior := make(priorVendorProjections)
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, agentsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s before compiling it: %w", agentsFile, err)
	}
	if exists {
		prior.add(data, clients)
	}
	if committed, ok := committedAgentsText(ctx, repoPath); ok {
		prior.add(committed, clients)
	}
	return prior, nil
}

// add records the digest of each projection of source, an AGENTS.md text, for the vendor files
// clients selects. A source that does not compile adds none.
func (prior priorVendorProjections) add(source []byte, clients []string) {
	tr := compiler.NewTranspiler()
	tr.Clients = clients
	res, err := tr.CompileContent(string(source))
	if err != nil {
		return
	}
	for i := 0; i < len(res.Files) && i < maxTranspileTargets; i++ {
		prior.record(res.Files[i].RelativePath, res.Files[i].Content, agentsFile)
	}
}

// record adds the digest of content, an earlier projection at rel compiled from source, to the
// texts a write over rel counts as Praetor output nobody edited. A text the digest refuses (not
// UTF-8, mixed line endings) adds none, so a file holding it counts as edited.
func (prior priorVendorProjections) record(rel, content, source string) {
	digest, _, err := util.CanonicalTextDigest([]byte(content))
	if err != nil {
		return
	}
	if prior[rel] == nil {
		prior[rel] = make(map[string]string, 2)
	}
	prior[rel][digest] = source
}

// committedAgentsText returns AGENTS.md as the HEAD commit holds it, read through the isolated
// git probe (util.RunGitProbe) and bounded like every text the writers accept. A repository
// without a commit, a HEAD without AGENTS.md, or a git that cannot answer yields none: an
// existing vendor file that matches no other projection then counts as edited and is backed
// up, the reading that loses nothing.
func committedAgentsText(ctx context.Context, repoPath string) ([]byte, bool) {
	result, err := util.RunGitProbe(ctx, repoPath, contextopt.MaxSourceBytes, "cat-file", "blob", headAgentsRevision)
	if err != nil {
		return nil, false
	}
	return result.Stdout, true
}

// isPriorProjection reports whether data is the projection of AGENTS.md as the run found it at
// rel, in one consistent line-ending style.
func (prior priorVendorProjections) isPriorProjection(rel string, data []byte) bool {
	_, known, _ := util.LookupCanonicalText(data, prior[rel])
	return known
}

// observeVendorTargets reads every compiled projection, a vendor context file or a persona
// copy, as it is before the write, through the root-pinned walk the projection writers use, so
// a symlinked directory is refused here too.
func observeVendorTargets(ctx context.Context, repoPath string, files []compiler.TargetFile) ([]vendorTarget, error) {
	targets := make([]vendorTarget, 0, len(files))
	for i := 0; i < len(files) && i < maxProjectionTargets; i++ {
		if _, err := repoFile(repoPath, files[i].RelativePath); err != nil {
			return nil, err
		}
		before, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, filepath.FromSlash(files[i].RelativePath))
		if err != nil {
			return nil, fmt.Errorf("projection target %s: %w", files[i].RelativePath, err)
		}
		targets = append(targets, vendorTarget{file: files[i], before: before, exists: exists})
	}
	return targets, nil
}

// replacesEdit reports whether writing t drops a hand edit: t existed and holds neither its
// new projection nor the prior one, in one consistent line-ending style.
func (t vendorTarget) replacesEdit(prior priorVendorProjections) bool {
	if !t.exists || t.file.Remove {
		return false
	}
	if same, err := util.CanonicalTextEquivalent(t.before, []byte(t.file.Content)); err == nil && same {
		return false
	}
	return !prior.isPriorProjection(t.file.RelativePath, t.before)
}

// projectionLabels names the report details of one kind of compiled projection: the vendor
// context files compiled from AGENTS.md (vendorContextLabels) or the persona and plugin copies
// projected from .agents (agentSurfaceLabels). The two share one record (projectionReplacements,
// recordProjections), so a file of either kind is created, synchronized or replaced by the same
// rule.
type projectionLabels struct {
	// compiled details a created or replaced file.
	compiled func(file compiler.TargetFile) string
	// synchronized details an existing file whose write dropped no hand edit.
	synchronized func(file compiler.TargetFile) string
}

// vendorContextLabels labels the vendor context files compiled from AGENTS.md.
var vendorContextLabels = projectionLabels{
	compiled: func(file compiler.TargetFile) string {
		return fmt.Sprintf("Compiled vendor context target (%d LOC)", file.LineCount)
	},
	synchronized: func(file compiler.TargetFile) string {
		return fmt.Sprintf("Synchronized vendor context target (%d LOC)", file.LineCount)
	},
}

// projectionReplacements returns a replacement for every target whose write drops a hand edit.
func projectionReplacements(targets []vendorTarget, prior priorVendorProjections, labels projectionLabels) []replacement {
	replaced := make([]replacement, 0, len(targets))
	for i := 0; i < len(targets) && i < maxProjectionTargets; i++ {
		if targets[i].replacesEdit(prior) {
			replaced = append(replaced, replacement{rel: targets[i].file.RelativePath, before: targets[i].before,
				after: []byte(targets[i].file.Content), detail: labels.compiled(targets[i].file)})
		}
	}
	return replaced
}

// recordProjections lists each target that replaced no hand edit: reconciled when it existed,
// created otherwise. replaceExistingAll records the others.
func recordProjections(report *AdoptReport, targets []vendorTarget, prior priorVendorProjections, labels projectionLabels) {
	for i := 0; i < len(targets) && i < maxProjectionTargets; i++ {
		target := targets[i]
		switch {
		case target.file.Remove:
			report.recordReconciledAs(target.file.RelativePath, actionRemove,
				"Removed the stale skill licence copy: the canonical skill carries no LICENSE")
		case target.replacesEdit(prior):
			continue
		case target.exists:
			report.recordReconciled(target.file.RelativePath, labels.synchronized(target.file))
		default:
			report.recordCreated(target.file.RelativePath, labels.compiled(target.file))
		}
	}
}

// preflightVendorBackupRoot refuses, on a run without --force, a backup root checkBackupRoot
// refuses when the agent-harness step may replace a hand-edited vendor file: an existing one
// that is not the projection of AGENTS.md as the run found it. That step backs such a file up
// mid-run, where a refused root used to fail after the earlier steps had written. The check is
// a superset of the step's own condition, since it cannot know the harness the step renders.
// Under --force preflightForceBackupRoot checks the root whatever the steps.
func preflightVendorBackupRoot(ctx context.Context, s *adoptSession) error {
	if s.opts.Force {
		return nil
	}
	declared, err := config.LoadDeclaredTooling(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
	}
	paths, _, err := agentcontext.TargetPaths(declared.AgentClients)
	if err != nil {
		return fmt.Errorf("agent_clients in %s: %w", manifestFile, err)
	}
	prior, err := priorVendorTexts(ctx, s.repoPath, declared.AgentClients)
	if err != nil {
		return err
	}
	for i := 0; i < len(paths) && i < maxTranspileTargets; i++ {
		before, exists, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(paths[i]))
		if err != nil {
			return fmt.Errorf("vendor context target %s: %w", paths[i], err)
		}
		if exists && !prior.isPriorProjection(paths[i], before) {
			return checkBackupRoot(ctx, s.repoPath)
		}
	}
	return nil
}
