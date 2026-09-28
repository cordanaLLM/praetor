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
// it (compile-context output nobody edited), is synchronized. Any other existing file holds a
// hand edit the rewrite drops, so it is replaced with a backup (replaceExistingAll).

// priorVendorProjections maps each vendor path to the digest (util.CanonicalTextDigest) of
// the projection of AGENTS.md as the run found it, in the form util.LookupCanonicalText reads.
type priorVendorProjections map[string]map[string]string

// vendorTarget is one vendor context file the agent-harness step writes, with the bytes it
// held before the write.
type vendorTarget struct {
	file   compiler.TargetFile
	before []byte
	exists bool
}

// priorVendorTexts compiles AGENTS.md as it is on disk for the vendor files clients selects
// and returns the digest of each projection. It runs before the agent-harness step rewrites
// AGENTS.md. An absent AGENTS.md, or one that does not compile (a foreign file adoption has
// not merged yet), yields none, so every existing vendor file that differs from the new
// projection counts as edited.
func priorVendorTexts(ctx context.Context, repoPath string, clients []string) (priorVendorProjections, error) {
	prior := make(priorVendorProjections)
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, agentsFile)
	if err != nil {
		return nil, fmt.Errorf("read %s before compiling it: %w", agentsFile, err)
	}
	if !exists {
		return prior, nil
	}
	tr := compiler.NewTranspiler()
	tr.Clients = clients
	if res, compileErr := tr.CompileContent(string(data)); compileErr == nil {
		for i := 0; i < len(res.Files) && i < maxTranspileTargets; i++ {
			if digest, _, digestErr := util.CanonicalTextDigest([]byte(res.Files[i].Content)); digestErr == nil {
				prior[res.Files[i].RelativePath] = map[string]string{digest: agentsFile}
			}
		}
	}
	return prior, nil
}

// isPriorProjection reports whether data is the projection of AGENTS.md as the run found it at
// rel, in one consistent line-ending style.
func (prior priorVendorProjections) isPriorProjection(rel string, data []byte) bool {
	_, known, _ := util.LookupCanonicalText(data, prior[rel])
	return known
}

// observeVendorTargets reads every compiled vendor file as it is before the write, through the
// root-pinned walk the vendor writer uses, so a symlinked directory is refused here too.
func observeVendorTargets(ctx context.Context, repoPath string, files []compiler.TargetFile) ([]vendorTarget, error) {
	targets := make([]vendorTarget, 0, len(files))
	for i := 0; i < len(files) && i < maxTranspileTargets; i++ {
		if _, err := repoFile(repoPath, files[i].RelativePath); err != nil {
			return nil, err
		}
		before, exists, err := contextopt.ObserveSnapshotIn(ctx, repoPath, filepath.FromSlash(files[i].RelativePath))
		if err != nil {
			return nil, fmt.Errorf("vendor context target %s: %w", files[i].RelativePath, err)
		}
		targets = append(targets, vendorTarget{file: files[i], before: before, exists: exists})
	}
	return targets, nil
}

// replacesEdit reports whether writing t drops a hand edit: t existed and holds neither its
// new projection nor the prior one, in one consistent line-ending style.
func (t vendorTarget) replacesEdit(prior priorVendorProjections) bool {
	if !t.exists {
		return false
	}
	if same, err := util.CanonicalTextEquivalent(t.before, []byte(t.file.Content)); err == nil && same {
		return false
	}
	return !prior.isPriorProjection(t.file.RelativePath, t.before)
}

// vendorReplacements returns a replacement for every target whose write drops a hand edit.
func vendorReplacements(targets []vendorTarget, prior priorVendorProjections) []replacement {
	replaced := make([]replacement, 0, len(targets))
	for i := 0; i < len(targets) && i < maxTranspileTargets; i++ {
		if targets[i].replacesEdit(prior) {
			replaced = append(replaced, replacement{rel: targets[i].file.RelativePath, before: targets[i].before,
				after: []byte(targets[i].file.Content), detail: compiledVendorDetail(targets[i].file)})
		}
	}
	return replaced
}

// compiledVendorDetail is the action detail of a vendor file compiled from AGENTS.md.
func compiledVendorDetail(file compiler.TargetFile) string {
	return fmt.Sprintf("Compiled vendor context target (%d LOC)", file.LineCount)
}

// recordVendorTargets lists each vendor file that replaced no hand edit: reconciled when it
// existed, created otherwise. replaceExistingAll records the others.
func recordVendorTargets(report *AdoptReport, targets []vendorTarget, prior priorVendorProjections) {
	for i := 0; i < len(targets) && i < maxTranspileTargets; i++ {
		target := targets[i]
		switch {
		case target.replacesEdit(prior):
			continue
		case target.exists:
			report.recordReconciled(target.file.RelativePath, fmt.Sprintf("Synchronized vendor context target (%d LOC)", target.file.LineCount))
		default:
			report.recordCreated(target.file.RelativePath, compiledVendorDetail(target.file))
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
