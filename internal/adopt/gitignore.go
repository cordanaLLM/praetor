package adopt

import (
	"context"
	"slices"
	"strings"

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

// configDirNegation re-includes the .config/ directory adoption writes its pinned catalog,
// label taxonomy, checkpoint policy and hook scripts to, where a Kconfig-style rule (a bare
// .config, /.config or .*) ignores it. The rule is directory-only, so a Kconfig .config file
// stays ignored at the root and at every depth. The managed block carries it only where such a
// rule hides the directory (kconfigConfigRule) and keeps it once written (mergeGitIgnoreRules).
const configDirNegation = "!" + configDir + "/"

// managedGitIgnoreRules returns the managed block's rules: managedIgnoreRules, then
// configDirNegation when negateConfig is set.
func managedGitIgnoreRules(negateConfig bool) []string {
	if !negateConfig {
		return managedIgnoreRules
	}
	return append(slices.Clone(managedIgnoreRules), configDirNegation)
}

// ManagedGitIgnoreBlock returns the canonical tail block shared by adoption and audit.
func ManagedGitIgnoreBlock() string {
	return gitIgnoreTailBlock().render(managedIgnoreRules)
}

// HasManagedGitIgnoreTail reports whether text, LF-normalized, ends with a canonical managed
// block: ManagedGitIgnoreBlock, or that block with configDirNegation, which adoption writes
// where a Kconfig-style rule hides .config/. Audit accepts either.
func HasManagedGitIgnoreTail(text string) bool {
	block := gitIgnoreTailBlock()
	return strings.HasSuffix(text, block.render(managedGitIgnoreRules(false))) ||
		strings.HasSuffix(text, block.render(managedGitIgnoreRules(true)))
}

// mergeGitIgnore owns one canonical tail block. Tail placement makes the private rules
// effective even when an adopter previously wrote negations; all bytes outside the block
// remain operator-owned. Exact unmarked rules are migrated from Praetor's former format.
func mergeGitIgnore(text string) (string, error) {
	return mergeGitIgnoreRules(text, false)
}

// mergeGitIgnoreRules is mergeGitIgnore with configDirNegation in the block when negateConfig
// is set or the block in text already holds it. Once written, the negation stays: a writer
// that does not probe (EnsurePrivateIgnore), and a probe the negation itself answers, would
// otherwise drop it and hide the directory again.
func mergeGitIgnoreRules(text string, negateConfig bool) (string, error) {
	block := gitIgnoreTailBlock()
	held, _, err := block.held(text)
	if err != nil {
		return "", err
	}
	negate := negateConfig || slices.Contains(held, configDirNegation)
	return block.merge(text, block.render(managedGitIgnoreRules(negate)))
}

// adoptGitIgnoreSeed is the file adoption starts a repository without .gitignore from: the
// build artefacts every flavour produces, ahead of the managed block.
const adoptGitIgnoreSeed = "bin/\n*.test\n*.out\n.DS_Store\n"

// reconcileGitIgnore appends the managed rules that are absent after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files. A dry run
// that plans the block records it (adoptSession.privateIgnorePlanned), so the backups it plans
// for later steps match the ones the real run takes once the block is written. Where a
// Kconfig-style rule hides .config/ (kconfigConfigRule), the block also re-includes that
// directory (configDirNegation) and the report names the rule; a dry run that plans it records
// it (adoptSession.configNegationPlanned), so the ignored-write check reports what the real run
// leaves ignored.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	rule, negate := kconfigConfigRule(ctx, s.repoPath)
	existed, changed, err := writeManagedGitIgnore(ctx, s.repoPath, adoptGitIgnoreSeed, s.opts.DryRun, negate)
	if err == nil && changed && s.opts.DryRun {
		s.privateIgnorePlanned = true
	}
	note := ""
	if negate {
		s.configNegationPlanned = s.opts.DryRun
		note = "; re-included the " + configDir + "/ directory Praetor writes to with " + configDirNegation +
			", because " + rule.Rule() + " ignores it (Kconfig " + configDir + " files stay ignored at every depth)"
	}
	switch {
	case err != nil:
		return err
	case !changed:
		s.report.recordReconciled(gitIgnoreFile, "Private artifact ignore tail block already effective")
	case existed:
		s.report.recordReconciledAs(gitIgnoreFile, actionAppend, "Preserved existing rules and excluded private working directory, gate worktrees and the AGY workspace configuration"+note)
	default:
		s.report.recordCreated(gitIgnoreFile, "Created ignore rules for build artifacts, private working directory, gate worktrees and the AGY workspace configuration"+note)
	}
	return nil
}

// writeManagedGitIgnore is the one read, merge and publish of the managed tail block;
// adoption and EnsurePrivateIgnore both go through it (HISS-19). seed is the content a
// repository without .gitignore starts from; negateConfig adds configDirNegation to the block
// (mergeGitIgnoreRules). It reports whether .gitignore existed and whether the merge changed
// it; a dry run reports the change without writing it.
func writeManagedGitIgnore(ctx context.Context, repoPath, seed string, dryRun, negateConfig bool) (existed, changed bool, err error) {
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
	merged, err := mergeGitIgnoreRules(text, negateConfig)
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
