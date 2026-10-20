package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
)

type commandTarget struct {
	dir    string
	gitDir bool
}

func resolveCommandRoot(ctx context.Context, root string, canonical Canonical, in Invocation) (string, error) {
	if Governed(root) || AppliesEverywhere(in.Settings) || canonical.Command == "" {
		return root, nil
	}
	target, err := commandTargetRoot(ctx, canonical.Command, commandBase(canonical, in.WorkDir))
	if err != nil {
		return "", err
	}
	if target == "" {
		return root, nil
	}
	return target, nil
}

// commandTargetRoot statically derives targets only when payload governance would skip under
// governed scope. It reuses forge's shell reader, never executes the command, and recognizes
// git global -C/--git-dir/--work-tree, leading GIT_DIR/GIT_WORK_TREE assignments, cd/pushd
// command words and env -C/--chdir. Relative paths use the first nonempty payload workspace,
// else Invocation.WorkDir; repeated git -C paths chain like Git, including an empty -C.
// Git directory options use the final -C directory. Other commands start from the payload
// directory: this is target discovery, not shell state simulation. One matching outer quote
// pair is stripped; escapes remain literal, preserving Windows path separators. Remaining
// quotes, $, backticks, ~user and other ~ forms, and glob marks are unresolved and ignored;
// a bare ~ or a leading ~/ resolves to the home directory (homeTarget). A subshell or group
// opener ahead of the command word, "(cd dir" or "{ cd dir", is read through.
// Missing paths, non-directories, malformed shell text,
// and derived Git probe errors are ignored; payload probe errors still fail closed. Exceeding
// the shared shell reader's field/line bounds fails closed so padding cannot hide operands.
// A git-dir named .git is probed through its parent work tree; other paths use repositoryRoot
// directly, so a bare/detached git-dir without a discoverable work tree is ignored. All probes
// use repositoryRoot's isolated, context-bounded RunGitProbe. Each syntactic directory operand
// counts, including duplicates and unresolved paths: more than maxWorkspaces fails closed
// before any target probe, even if an earlier target is governed. Otherwise the first
// governed target wins; no match preserves the original workspace skip reason.
func commandTargetRoot(ctx context.Context, command, base string) (string, error) {
	fields, err := derivedShellFields(command)
	if err != nil {
		return "", err
	}
	var targets []commandTarget
	start := 0
	for i := 0; i <= len(fields); i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		targets = append(targets, commandDirectories(fields[start:i], base)...)
		if len(targets) > maxWorkspaces {
			return "", fmt.Errorf("command names more than %d command targets", maxWorkspaces)
		}
		start = i + 1
	}
	return governedCommandTarget(ctx, targets), nil
}

func derivedShellFields(command string) ([]string, error) {
	fields, err := forge.ShellFields(command)
	if errors.Is(err, forge.ErrShellReadLimit) {
		return nil, err
	}
	// Other parse failures return nil fields and retain the original workspace skip.
	return fields, nil
}

func targetCommandSeparator(word string) bool {
	switch word {
	case ";", "&", "&&", "|", "||":
		return true
	default:
		return false
	}
}

func governedCommandTarget(ctx context.Context, targets []commandTarget) string {
	for i := 0; i < len(targets) && i < maxWorkspaces; i++ {
		dir := targets[i].dir
		if dir == "" {
			continue
		}
		if targets[i].gitDir && filepath.Base(dir) == ".git" {
			dir = filepath.Dir(dir)
		}
		root, err := repositoryRoot(ctx, dir)
		if err == nil && Governed(root) {
			return root
		}
	}
	return ""
}

func commandBase(canonical Canonical, workDir string) string {
	for i := 0; i < len(canonical.Workspaces) && i < maxWorkspaces; i++ {
		if canonical.Workspaces[i] != "" {
			return canonical.Workspaces[i]
		}
	}
	return workDir
}

func targetDirectory(value, base string) string {
	value = homeTarget(unquoteTarget(value))
	if value == "" || strings.ContainsAny(value, "$`~*?[]{}'\"") {
		return ""
	}
	if !filepath.IsAbs(value) {
		if base == "" {
			return ""
		}
		value = filepath.Join(base, value)
	}
	dir, err := filepath.EvalSymlinks(value)
	if err != nil {
		return ""
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// homeTarget expands a bare ~ or a leading ~/ to the user's home directory, as the shell does
// for the same user the hook runs as. ~user and a ~ anywhere else stay unexpanded, and a home
// directory that cannot be read leaves the value as it is, so targetDirectory ignores it.
func homeTarget(value string) string {
	if value != "~" && !strings.HasPrefix(value, "~/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	return filepath.Join(home, value[1:])
}

func unquoteTarget(value string) string {
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}
