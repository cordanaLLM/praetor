package adopt

import (
	"context"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// agyWorkspaceIgnore is the Antigravity workspace MCP configuration, written
// per host by `clients apply --client agy`; it carries host paths and is never
// tracked.
const agyWorkspaceIgnore = "/.agents/mcp_config.json"

// managedIgnoreRules are the ignore rules adoption guarantees in every adopted
// repository: the private session ledger, the container for the isolated gate
// worktrees, whose leftovers would otherwise be scanned as repository content
// and would keep the tree dirty for receipt minting, and the per-host AGY
// workspace configuration.
var managedIgnoreRules = []string{"/.workingdir/", "/" + worktree.WorktreeSubdir + "/", agyWorkspaceIgnore}

// missingIgnoreRules reports the managed rules that text does not already carry.
// It compares whole lines rather than testing the end of the file, so a rule that
// other entries were appended after still counts as present and is never duplicated.
func missingIgnoreRules(text string) []string {
	present := make(map[string]struct{}, strings.Count(text, "\n")+1)
	for _, line := range strings.Split(text, "\n") {
		present[strings.TrimSpace(line)] = struct{}{}
	}
	missing := make([]string, 0, len(managedIgnoreRules))
	for _, rule := range managedIgnoreRules {
		if _, ok := present[rule]; !ok {
			missing = append(missing, rule)
		}
	}
	return missing
}

// reconcileGitIgnore appends the managed rules that are absent after any older opt-ins,
// preserving existing bytes. It never untracks or removes existing private files.
func reconcileGitIgnore(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, gitIgnoreFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	text := string(data)
	missing := missingIgnoreRules(text)
	if len(missing) == 0 {
		s.report.recordReconciled(gitIgnoreFile, "Private working directory, gate worktree and AGY workspace ignore rules preserved")
		return nil
	}
	if !exists {
		text = "bin/\n*.test\n*.out\n.DS_Store\n"
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += strings.Join(missing, "\n") + "\n"
	if !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, full, []byte(text), contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm}); err != nil {
			return err
		}
	}
	if exists {
		s.report.recordReconciledAs(gitIgnoreFile, actionAppend, "Preserved existing rules and excluded private working directory, gate worktrees and the AGY workspace configuration")
	} else {
		s.report.recordCreated(gitIgnoreFile, "Created ignore rules for build artifacts, private working directory, gate worktrees and the AGY workspace configuration")
	}
	return nil
}
