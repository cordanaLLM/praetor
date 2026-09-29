package adopt

import (
	"context"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// agyWorkspaceIgnore is the Antigravity workspace MCP configuration, written
// per host by `clients apply --client agy`; it carries host paths and is never
// tracked.
const agyWorkspaceIgnore = "/.agents/mcp_config.json"

const (
	gitIgnoreManagedBegin = "# BEGIN praetor private artifacts (praetorctl adopt)"
	gitIgnoreManagedEnd   = "# END praetor private artifacts"
	maxGitIgnoreLines     = 4096
)

// managedIgnoreRules are the ignore rules adoption guarantees in every adopted
// repository: the private session ledger, the container for the isolated gate
// worktrees, whose leftovers would otherwise be scanned as repository content
// and would keep the tree dirty for receipt minting, and the per-host AGY
// workspace configuration.
var managedIgnoreRules = []string{"/.workingdir/", "/.workingdir2/", "/" + worktree.WorktreeSubdir + "/", agyWorkspaceIgnore}

// gitIgnoreTailBlock is the private-artifact block adoption owns at the tail of .gitignore.
// Exact unmarked rules Praetor's former format wrote are migrated into it.
func gitIgnoreTailBlock() managedTailBlock {
	return managedTailBlock{
		file: gitIgnoreFile, begin: gitIgnoreManagedBegin, end: gitIgnoreManagedEnd,
		maxLines: maxGitIgnoreLines, duplicate: "duplicate managed markers", legacy: managedIgnoreRules,
	}
}

// ManagedGitIgnoreBlock returns the canonical tail block shared by adoption and audit.
func ManagedGitIgnoreBlock() string {
	return gitIgnoreTailBlock().render(managedIgnoreRules)
}

// mergeGitIgnore owns one canonical tail block. Tail placement makes the private rules
// effective even when an adopter previously wrote negations; all bytes outside the block
// remain operator-owned. Exact unmarked rules are migrated from Praetor's former format.
func mergeGitIgnore(text string) (string, error) {
	return gitIgnoreTailBlock().merge(text, ManagedGitIgnoreBlock())
}

// adoptGitIgnoreSeed is the file adoption starts a repository without .gitignore from: the
// build artefacts every flavour produces, ahead of the managed block.
const adoptGitIgnoreSeed = "bin/\n*.test\n*.out\n.DS_Store\n"

// reconcileGitIgnore appends the managed rules that are absent after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files. A dry run
// that plans the block records it (adoptSession.privateIgnorePlanned), so the backups it plans
// for later steps match the ones the real run takes once the block is written.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	existed, changed, err := writeManagedGitIgnore(ctx, s.repoPath, adoptGitIgnoreSeed, s.opts.DryRun)
	if err == nil && changed && s.opts.DryRun {
		s.privateIgnorePlanned = true
	}
	switch {
	case err != nil:
		return err
	case !changed:
		s.report.recordReconciled(gitIgnoreFile, "Private artifact ignore tail block already effective")
	case existed:
		s.report.recordReconciledAs(gitIgnoreFile, actionAppend, "Preserved existing rules and excluded private working directory, gate worktrees and the AGY workspace configuration")
	default:
		s.report.recordCreated(gitIgnoreFile, "Created ignore rules for build artifacts, private working directory, gate worktrees and the AGY workspace configuration")
	}
	return nil
}

// writeManagedGitIgnore is the one read, merge and publish of the managed tail block;
// adoption and EnsurePrivateIgnore both go through it (HISS-19). seed is the content a
// repository without .gitignore starts from. It reports whether .gitignore existed and
// whether the merge changed it; a dry run reports the change without writing it.
func writeManagedGitIgnore(ctx context.Context, repoPath, seed string, dryRun bool) (existed, changed bool, err error) {
	full, err := repoFile(repoPath, gitIgnoreFile)
	if err != nil {
		return false, false, err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return false, false, err
	}
	text := string(data)
	if !exists {
		text = seed
	}
	merged, err := mergeGitIgnore(text)
	if err != nil {
		return exists, false, err
	}
	if exists && merged == string(data) {
		return true, false, nil
	}
	if dryRun {
		return exists, true, nil
	}
	options := contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm}
	return exists, true, contextopt.ReplaceSnapshot(ctx, full, []byte(merged), options)
}
