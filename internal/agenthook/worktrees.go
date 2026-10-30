// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/topology"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Worktree location enforcement for `git worktree add` (order
// praetor-rules-24h-ops-lesson-checks-20261028). When hooks.worktrees.enforce is true,
// the pre-tool command policy refuses `git [-C <dir>] worktree add [options] <path>
// [<commit-ish>]` unless <path>, resolved against -C <dir> or the hook working directory,
// lies directly under <dev root>/worktrees/<repository folder>-<topic> (with a non-empty
// topic) or under <repository root>/.claude/worktrees/<name>. Unparsable `git worktree add`
// commands fail closed with the parse reason while enforcement is on.

var worktreeAddBoolLongOpts = []string{
	"--force",
	"--detach",
	"--checkout",
	"--no-checkout",
	"--lock",
	"--no-lock",
	"--orphan",
	"--track",
	"--no-track",
	"--guess-remote",
	"--no-guess-remote",
	"--quiet",
	"--relative-paths",
	"--no-relative-paths",
	"--no-reason",
}

type worktreeCheck struct{}

func newWorktreeCheck(settings config.WorktreeSettings) *worktreeCheck {
	if !settings.Enforce {
		return nil
	}
	return &worktreeCheck{}
}

func (c *worktreeCheck) judge(ctx context.Context, command, workDir string) Verdict {
	ctx, cancel := context.WithTimeout(ctx, preToolBudget)
	defer cancel()
	fields, err := forge.ShellFields(command)
	if err != nil {
		return Verdict{Outcome: Deny, Reason: boundReason(refusal("HISS", "git worktree add check: "+err.Error()))}
	}
	for start, i := 0, 0; i <= len(fields); i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		if reason := judgeWorktreeStatement(ctx, fields[start:i], workDir); reason != "" {
			return Verdict{Outcome: Deny, Reason: boundReason(refusal("HISS", reason))}
		}
		start = i + 1
	}
	return Verdict{Outcome: Allow}
}

func judgeWorktreeStatement(ctx context.Context, words []string, workDir string) string {
	gitWords, base, isGit := findGitWords(words, workDir)
	if !isGit {
		return ""
	}
	addArgs, resolvedBase, dirErr, isAdd := gitWorktreeAddArgs(gitWords, base)
	if !isAdd {
		return ""
	}
	rawPath, parseErr := parseWorktreeAddPath(addArgs)
	if parseErr != "" {
		return parseErr
	}
	if dirErr != "" {
		return dirErr
	}
	return judgeWorktreeTarget(ctx, rawPath, resolvedBase)
}

func findGitWords(words []string, base string) ([]string, string, bool) {
	for i := 0; i < len(words); i++ {
		if _, _, assignment := forge.ShellAssignment(words[i]); assignment {
			continue
		}
		word := strings.TrimLeft(unquoteTarget(words[i]), "(")
		switch {
		case word == "" || word == "{":
			continue
		case word == "env":
			i, base, _ = envDirectories(words, i+1, base)
			i--
		case commandName(word) == "git":
			return words[i+1:], base, true
		default:
			return nil, "", false
		}
	}
	return nil, "", false
}

func gitWorktreeAddArgs(words []string, base string) (args []string, resolvedBase, dirErr string, isAdd bool) {
	i := 0
	for ; i < len(words); i++ {
		option, value, next := directoryOption(words, i)
		if option == "--" {
			i++
			break
		}
		if !strings.HasPrefix(option, "-") {
			break
		}
		i = next
		if option == "-C" && unquoteTarget(value) != "" && dirErr == "" {
			resolved := targetDirectory(value, base)
			if resolved == "" {
				dirErr = fmt.Sprintf("git worktree add: cannot resolve -C directory %q", unquoteTarget(value))
			}
			base = resolved
		}
	}
	if i+1 >= len(words) || unquoteTarget(words[i]) != "worktree" || unquoteTarget(words[i+1]) != "add" {
		return nil, "", "", false
	}
	return words[i+2:], base, dirErr, true
}

func parseWorktreeAddPath(args []string) (string, string) {
	var positional []string
	for i := 0; i < len(args); i++ {
		word := unquoteTarget(args[i])
		if word == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(word, "-") && word != "-" {
			next, errReason := consumeWorktreeAddOption(word, args, i)
			if errReason != "" {
				return "", errReason
			}
			i = next
			continue
		}
		positional = append(positional, args[i])
	}
	if len(positional) == 0 {
		return "", "git worktree add: missing <path>"
	}
	if len(positional) > 2 {
		return "", "git worktree add: unexpected positional argument " + unquoteTarget(positional[2])
	}
	return positional[0], ""
}

func consumeWorktreeAddOption(word string, args []string, i int) (int, string) {
	if strings.HasPrefix(word, "--") {
		return consumeWorktreeAddLongOption(word, args, i)
	}
	return consumeWorktreeAddShortOptions(word, args, i)
}

func consumeWorktreeAddLongOption(word string, args []string, i int) (int, string) {
	if slices.Contains(worktreeAddBoolLongOpts, word) {
		return i, ""
	}
	if val, ok := strings.CutPrefix(word, "--reason="); ok {
		if val == "" {
			return i, "git worktree add: missing argument for --reason"
		}
		return i, ""
	}
	if word == "--reason" {
		if i+1 >= len(args) || unquoteTarget(args[i+1]) == "" {
			return i, "git worktree add: missing argument for --reason"
		}
		return i + 1, ""
	}
	return i, "git worktree add: unknown option " + word
}

func consumeWorktreeAddShortOptions(word string, args []string, i int) (int, string) {
	flags := word[1:]
	for j := 0; j < len(flags); j++ {
		switch flags[j] {
		case 'f', 'q', 'd':
			continue
		case 'b', 'B':
			if j+1 < len(flags) {
				return i, ""
			}
			if i+1 >= len(args) || unquoteTarget(args[i+1]) == "" {
				return i, "git worktree add: missing branch name for -" + string(flags[j])
			}
			return i + 1, ""
		default:
			return i, "git worktree add: unknown option " + word
		}
	}
	return i, ""
}

func judgeWorktreeTarget(ctx context.Context, rawPath, baseDir string) string {
	resolvedBase, repoRoot, devRoot, errReason := resolveWorktreeContext(ctx, baseDir)
	if errReason != "" {
		return errReason
	}
	targetPath, errReason := resolveWorktreeTargetPath(ctx, rawPath, resolvedBase)
	if errReason != "" {
		return errReason
	}
	if allowedWorktreePath(targetPath, devRoot, repoRoot) {
		return ""
	}
	worktreeForm := filepath.Join(devRoot, topology.WorktreesContainer, filepath.Base(repoRoot)+"-<topic>")
	claudeForm := filepath.Join(repoRoot, ".claude", topology.WorktreesContainer, "<name>")
	return fmt.Sprintf("git worktree add target %s outside declared worktree locations; allowed: %s or %s",
		targetPath, worktreeForm, claudeForm)
}

func resolveWorktreeContext(ctx context.Context, baseDir string) (resolvedBase, repoRoot, devRoot, errReason string) {
	if baseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", "", "", "git worktree add: cannot resolve working directory: " + err.Error()
		}
		baseDir = wd
	}
	resolvedBase = targetDirectory(baseDir, "")
	if resolvedBase == "" {
		return "", "", "", fmt.Sprintf("git worktree add: working directory %q does not exist", baseDir)
	}
	repoRoot, err := topology.RepositoryRoot(ctx, resolvedBase)
	if err != nil {
		return "", "", "", fmt.Sprintf("git worktree add: cannot resolve repository for %s: %v", resolvedBase, err)
	}
	rawDevRoot, err := topology.ResolveDevRootDir("", topology.DevRootEnv)
	if err != nil {
		return "", "", "", "git worktree add: cannot resolve dev root: " + err.Error()
	}
	devRoot, err = util.ResolveExistingPath(ctx, rawDevRoot)
	if err != nil {
		return "", "", "", "git worktree add: cannot resolve dev root: " + err.Error()
	}
	return resolvedBase, repoRoot, devRoot, ""
}

func resolveWorktreeTargetPath(ctx context.Context, rawPath, baseDir string) (string, string) {
	cleaned := homeTarget(unquoteTarget(rawPath))
	if cleaned == "" {
		return "", "git worktree add: missing <path>"
	}
	if strings.ContainsAny(cleaned, "$`~*?[]{}'\"") {
		return "", fmt.Sprintf("git worktree add: path %q contains unresolved shell syntax", rawPath)
	}
	if !filepath.IsAbs(cleaned) {
		cleaned = filepath.Join(baseDir, cleaned)
	}
	cleaned = filepath.Clean(cleaned)
	if !filepath.IsAbs(cleaned) {
		return cleaned, ""
	}
	resolved, err := util.ResolveExistingPath(ctx, cleaned)
	if err != nil {
		return "", fmt.Sprintf("git worktree add: cannot resolve target path %q: %v", rawPath, err)
	}
	return resolved, ""
}

func allowedWorktreePath(targetPath, devRoot, repoRoot string) bool {
	worktreesDir := filepath.Join(devRoot, topology.WorktreesContainer)
	repoName := filepath.Base(repoRoot)
	if filepath.Dir(targetPath) == worktreesDir && hasRepoTopicPrefix(filepath.Base(targetPath), repoName) {
		return true
	}
	claudeWorktreesDir := filepath.Join(repoRoot, ".claude", topology.WorktreesContainer)
	return withinClaudeWorktrees(targetPath, claudeWorktreesDir)
}

func hasRepoTopicPrefix(baseName, repoName string) bool {
	topic, ok := strings.CutPrefix(baseName, repoName+"-")
	return ok && topic != "" && topic != "." && topic != ".."
}

func withinClaudeWorktrees(targetPath, claudeWorktreesDir string) bool {
	return util.WithinRoot(claudeWorktreesDir, targetPath) && targetPath != claudeWorktreesDir
}
