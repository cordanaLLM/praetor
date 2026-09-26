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

// ManagedGitIgnoreBlock returns the canonical tail block shared by adoption and audit.
func ManagedGitIgnoreBlock() string {
	return gitIgnoreManagedBegin + "\n" + strings.Join(managedIgnoreRules, "\n") + "\n" +
		gitIgnoreManagedEnd + "\n"
}

// mergeGitIgnore owns one canonical tail block. Tail placement makes the private rules
// effective even when an adopter previously wrote negations; all bytes outside the block
// remain operator-owned. Exact unmarked rules are migrated from Praetor's former format.
func mergeGitIgnore(text string) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(text)
	if err != nil {
		return "", fmt.Errorf("%s line endings are inconsistent: %w", gitIgnoreFile, err)
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) > maxGitIgnoreLines {
		return "", fmt.Errorf("%s exceeds %d lines", gitIgnoreFile, maxGitIgnoreLines)
	}
	kept, err := filterManagedGitIgnoreLines(lines)
	if err != nil {
		return "", err
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if body != "" {
		body += "\n\n"
	}
	return util.RestoreLineEndings(body+ManagedGitIgnoreBlock(), crlf), nil
}

type gitIgnoreScan struct {
	kept    []string
	inBlock bool
	seen    bool
}

func filterManagedGitIgnoreLines(lines []string) ([]string, error) {
	scan := gitIgnoreScan{kept: make([]string, 0, len(lines))}
	for index := 0; index < len(lines) && index < maxGitIgnoreLines; index++ {
		if err := scan.consume(lines[index], index); err != nil {
			return nil, err
		}
	}
	if scan.inBlock {
		return nil, fmt.Errorf("%s contains an unterminated managed block", gitIgnoreFile)
	}
	return scan.kept, nil
}

func (scan *gitIgnoreScan) consume(line string, index int) error {
	trimmed := strings.TrimSpace(line)
	if (trimmed == gitIgnoreManagedBegin || trimmed == gitIgnoreManagedEnd) && trimmed != line {
		return fmt.Errorf("%s contains a non-canonical managed marker at line %d", gitIgnoreFile, index+1)
	}
	switch {
	case line == gitIgnoreManagedBegin:
		if scan.inBlock || scan.seen {
			return fmt.Errorf("%s contains duplicate managed markers", gitIgnoreFile)
		}
		scan.inBlock, scan.seen = true, true
	case line == gitIgnoreManagedEnd:
		if !scan.inBlock {
			return fmt.Errorf("%s contains an unmatched managed end marker", gitIgnoreFile)
		}
		scan.inBlock = false
	case !scan.inBlock && !slices.Contains(managedIgnoreRules, line):
		scan.kept = append(scan.kept, line)
	}
	return nil
}

// adoptGitIgnoreSeed is the file adoption starts a repository without .gitignore from: the
// build artefacts every flavour produces, ahead of the managed block.
const adoptGitIgnoreSeed = "bin/\n*.test\n*.out\n.DS_Store\n"

// reconcileGitIgnore appends the managed rules that are absent after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	existed, changed, err := writeManagedGitIgnore(ctx, s.repoPath, adoptGitIgnoreSeed, s.opts.DryRun)
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
