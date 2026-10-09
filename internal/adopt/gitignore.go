package adopt

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// agyWorkspaceIgnore is the Antigravity workspace MCP configuration, written
// per host by `clients apply --client agy`; it carries host paths and is never
// tracked.
const agyWorkspaceIgnore = "/.agents/mcp_config.json"

// mcpOutputCacheIgnore is the cache directory standards-mcp offloads large tool output to
// (internal/mcp.OffloadCacheDir). It is host-local output and is never tracked.
const mcpOutputCacheIgnore = "/.standards/cache/"

const (
	gitIgnoreManagedBegin = "# BEGIN praetor private artifacts (praetorctl adopt)"
	gitIgnoreManagedEnd   = "# END praetor private artifacts"
	maxGitIgnoreLines     = 4096
)

// managedIgnoreRules are the ignore rules of the managed block, in block order: the
// private session ledger, the legacy scratch root, the container for the isolated gate
// worktrees, whose leftovers would otherwise be scanned as repository content and would
// keep the tree dirty for receipt minting, the per-host AGY workspace configuration, and the
// standards-mcp output cache.
// Adoption guarantees each in every adopted repository, except that a repository may
// retire legacyScratchIgnore (keepsLegacyScratch). The whole list is also the set of
// unmarked lines Praetor's former format wrote.
var managedIgnoreRules = []string{"/.workingdir/", legacyScratchIgnore, "/" + worktree.WorktreeSubdir + "/", agyWorkspaceIgnore, mcpOutputCacheIgnore}

// gitIgnoreBlockVersion identifies an accepted version of the managed .gitignore block.
type gitIgnoreBlockVersion struct {
	Version string
	Rules   []string
}

// canonicalGitIgnoreBlockVersion is the current canonical version of the managed .gitignore block.
var canonicalGitIgnoreBlockVersion = gitIgnoreBlockVersion{
	Version: "v2",
	Rules:   managedIgnoreRules,
}

// gitIgnoreBlockHistory lists earlier accepted versions of the managed .gitignore block,
// in chronological order (oldest first). Each entry is stored as a literal rule list
// to prevent aliasing with live canonical rules.
var gitIgnoreBlockHistory = []gitIgnoreBlockVersion{
	{
		Version: "v1",
		Rules: []string{
			"/.workingdir/",
			legacyScratchIgnore,
			"/" + worktree.WorktreeSubdir + "/",
			agyWorkspaceIgnore,
		},
	},
}

// GitIgnoreTailMatch describes whether text matches an accepted managed tail block.
type GitIgnoreTailMatch struct {
	Matched bool
	Current bool
	Version string
}

// RulesFor returns the version's rules adjusted for keepLegacy and negateConfig.
// It returns a cloned slice to ensure the version's underlying rules cannot be mutated.
func (v gitIgnoreBlockVersion) RulesFor(keepLegacy, negateConfig bool) []string {
	rules := privateIgnoreRules(v.Rules, keepLegacy)
	if negateConfig {
		return append(rules, configDirNegation)
	}
	return rules
}

// Render returns the version's managed tail block for the given configuration.
func (v gitIgnoreBlockVersion) Render(keepLegacy, negateConfig bool) string {
	return gitIgnoreTailBlock().render(v.RulesFor(keepLegacy, negateConfig))
}

// maxHistoryVersions bounds the historical block versions checked (HISS-02).
const maxHistoryVersions = 64

// LookupManagedGitIgnoreTail inspects text (the .gitignore content of the repository at repoPath)
// and reports whether it ends with an accepted managed private-artifact block: the current
// canonical block or a known historical version in gitIgnoreBlockHistory.
func LookupManagedGitIgnoreTail(repoPath, text string) (GitIgnoreTailMatch, bool) {
	normalized, _ := util.NormalizeLineEndings(text)
	keep := keepsLegacyScratch(normalized, legacyScratchPresent(repoPath), false)

	if versionMatchesTail(normalized, canonicalGitIgnoreBlockVersion, keep) {
		return GitIgnoreTailMatch{
			Matched: true,
			Current: true,
			Version: canonicalGitIgnoreBlockVersion.Version,
		}, true
	}

	for i := len(gitIgnoreBlockHistory) - 1; i >= 0 && i < maxHistoryVersions; i-- {
		v := gitIgnoreBlockHistory[i]
		if versionMatchesTail(normalized, v, keep) {
			return GitIgnoreTailMatch{
				Matched: true,
				Current: false,
				Version: v.Version,
			}, true
		}
	}

	return GitIgnoreTailMatch{}, false
}

// versionMatchesTail reports whether normalized text ends with version's block (with or without configDirNegation).
func versionMatchesTail(normalized string, v gitIgnoreBlockVersion, keepLegacy bool) bool {
	return strings.HasSuffix(normalized, v.Render(keepLegacy, false)) ||
		strings.HasSuffix(normalized, v.Render(keepLegacy, true))
}

// checkBlockHistoryTransition validates that whenever the canonical block changes from
// previous to current, previous is present in history.
func checkBlockHistoryTransition(previous, current gitIgnoreBlockVersion, history []gitIgnoreBlockVersion) error {
	if slices.Equal(previous.Rules, current.Rules) {
		return nil
	}
	for i := 0; i < len(history) && i < maxHistoryVersions; i++ {
		if slices.Equal(history[i].Rules, previous.Rules) {
			return nil
		}
	}
	return fmt.Errorf("canonical block changed from %s without appending it to history", previous.Version)
}

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
// stays ignored at the root and at every depth, and anchored, so it re-includes the root
// directory alone: a .config/ directory deeper in the tree (a vendored tool's) stays ignored.
// It is the form negationFor proposes for any ignored directory. The managed block carries it
// only where such a rule hides the directory (kconfigConfigRule) and keeps it once written
// (mergeGitIgnoreRules); a file below the directory that adoption does not write is re-included
// with it and named (reportReincludedConfigFiles).
const configDirNegation = "!/" + configDir + "/"

// managedGitIgnoreRules returns the managed block's rules: canonicalGitIgnoreBlockVersion.Rules,
// without legacyScratchIgnore unless keepLegacy is set, then configDirNegation when negateConfig is set.
func managedGitIgnoreRules(keepLegacy, negateConfig bool) []string {
	return canonicalGitIgnoreBlockVersion.RulesFor(keepLegacy, negateConfig)
}

// renderManagedGitIgnore renders the managed block from managedGitIgnoreRules.
func renderManagedGitIgnore(keepLegacy, negateConfig bool) string {
	return gitIgnoreTailBlock().render(managedGitIgnoreRules(keepLegacy, negateConfig))
}

// ManagedGitIgnoreBlock returns the canonical tail block with every managed rule: the block
// adoption writes into a .gitignore that holds none yet.
func ManagedGitIgnoreBlock() string {
	return renderManagedGitIgnore(true, false)
}

// RetiredLegacyScratchBlock returns ManagedGitIgnoreBlock without legacyScratchIgnore: the
// block of a repository that retired the legacy scratch root (keepsLegacyScratch).
func RetiredLegacyScratchBlock() string {
	return renderManagedGitIgnore(false, false)
}

// HasManagedGitIgnoreTail reports whether text, the LF-normalized .gitignore of the repository
// at repoPath, ends with the canonical managed block adoption writes there: the current canonical
// block or an earlier accepted version in gitIgnoreBlockHistory. Audit and verify paths route
// through LookupManagedGitIgnoreTail.
func HasManagedGitIgnoreTail(repoPath, text string) bool {
	_, ok := LookupManagedGitIgnoreTail(repoPath, text)
	return ok
}

// mergeGitIgnore owns one canonical tail block. Tail placement makes the private rules
// effective even when an adopter previously wrote negations; all bytes outside the block
// remain operator-owned. Exact unmarked rules are migrated from Praetor's former format. It is
// mergeGitIgnoreRules with no legacy scratch root on disk and no configDirNegation request.
func mergeGitIgnore(text string) (string, error) {
	return mergeGitIgnoreRules(text, false, false)
}

// mergeGitIgnoreRules is mergeGitIgnore for a repository whose legacy scratch root is present
// or not (legacyPresent), which with text decides whether the block keeps legacyScratchIgnore
// (keepsLegacyScratch). The block carries configDirNegation when negateConfig is set or the
// block in text already holds it. Once written, the negation stays: a writer that does not
// probe (EnsurePrivateIgnore), and a probe the negation itself answers, would otherwise drop it
// and hide the directory again.
func mergeGitIgnoreRules(text string, legacyPresent, negateConfig bool) (string, error) {
	block := gitIgnoreTailBlock()
	held, _, err := block.held(text)
	if err != nil {
		return "", err
	}
	negate := negateConfig || slices.Contains(held, configDirNegation)
	return block.merge(text, renderManagedGitIgnore(keepsLegacyScratch(text, legacyPresent, false), negate))
}

// adoptGitIgnoreSeed is the file adoption starts a repository without .gitignore from: the
// build artefacts every flavour produces, ahead of the managed block.
const adoptGitIgnoreSeed = "bin/\n*.test\n*.out\n.DS_Store\n"

// reconcileGitIgnore appends the managed rules that are absent after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files. A dry run
// that plans the block records it (adoptSession.privateIgnorePlanned), so the backups it plans
// for later steps match the ones the real run takes once the block is written. Where a
// Kconfig-style rule hides .config/ (kconfigConfigRule), the block also re-includes that
// directory (configDirNegation) and the report names the rule; the session records it
// (adoptSession.configNegationAdded), so the ignored-write check of a dry run reports what the
// real run leaves ignored and every run names the other files the negation re-includes.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	rule, negate := kconfigConfigRule(ctx, s.repoPath)
	existed, changed, err := writeManagedGitIgnore(ctx, s.repoPath, adoptGitIgnoreSeed, s.opts.DryRun, negate)
	if err == nil && changed && s.opts.DryRun {
		s.privateIgnorePlanned = true
	}
	note := ""
	if negate {
		s.configNegationAdded = err == nil
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
// repository without .gitignore starts from; negateConfig adds configDirNegation to the block,
// and the legacy scratch rule stays while keepsLegacyScratch requires it for the repository
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
	merged, err := mergeGitIgnoreRules(text, legacyScratchPresent(repoPath), negateConfig)
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
