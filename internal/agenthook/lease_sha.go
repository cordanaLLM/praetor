// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// literalCommitSHA accepts only a complete literal commit abbreviation or SHA-1 name.
var literalCommitSHA = regexp.MustCompile(`\A[0-9a-fA-F]{7,40}\z`)

// rawSHAHexRun detects a potentially literal commit even when shell parsing fails.
var rawSHAHexRun = regexp.MustCompile(`[0-9a-fA-F]{7,}`)

// rawSHAForm matches whole SHA-bearing names; flags and script names cannot match prefixes.
var rawSHAForm = regexp.MustCompile(`(?:\A|[^-\w])(?:--force-with-lease|--match-head-commit|--commit)(?:\z|[^-\w])|` +
	`(?:\A|[^-\w.])squash_merge\.sh(?:\z|[^-\w.])|` +
	`(?:\A|[^-\w])git[^-\w](?:[\s\S]*[^-\w])?(?:log|show)(?:\z|[^-\w])|` +
	`(?:\A|[^-\w])gh[^-\w](?:[\s\S]*[^-\w])?api[^-\w](?:[\s\S]*[/\s'"])?commits/`)

// commitSHAOperand retains the source of a literal for an actionable refusal.
type commitSHAOperand struct {
	value string
	flag  string
}

// judgeLiteralCommitSHAs resolves literal operands before command execution.
// One shared three-second budget bounds every Git read across the entire shell command.
// Parse errors refuse only raw SHA-bearing forms accompanied by seven or more hex digits.
func judgeLiteralCommitSHAs(ctx context.Context, command, workDir string) Verdict {
	fields, err := forge.ShellFields(command)
	if err != nil {
		if rawSHAForm.MatchString(command) && rawSHAHexRun.MatchString(command) {
			return shaRefusal("command parse: " + err.Error())
		}
		return Verdict{Outcome: Allow}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if workDir == "" {
		workDir, err = os.Getwd()
		if err != nil {
			return shaRefusal("repository working directory: " + err.Error())
		}
	}
	stack := []string{workDir}
	start := 0
	for i := 0; i <= len(fields); i++ {
		if i < len(fields) && !targetCommandSeparator(fields[i]) {
			continue
		}
		words := fields[start:i]
		stack = pushSubshellLevel(stack, countSubshellOpeners(words))
		segment := trimSegmentTrailingParens(words, countSubshellClosers(words))
		nextDir, reason := judgeSHAStatement(ctx, segment, stack[len(stack)-1])
		if reason != "" {
			return shaRefusal(reason)
		}
		stack[len(stack)-1] = nextDir
		stack = popSubshellLevel(stack, countSubshellClosers(words))
		start = i + 1
	}
	return Verdict{Outcome: Allow}
}

// judgeLiteralCommitSHAsAfter preserves prior refusals and notices before inspecting SHAs.
func judgeLiteralCommitSHAsAfter(ctx context.Context, command, workDir string, previous Verdict) Verdict {
	if previous.Outcome != Allow {
		return previous
	}
	verdict := judgeLiteralCommitSHAs(ctx, joinLineContinuations(command), workDir)
	verdict.Notice = appendLaneNotice(previous.Notice, verdict.Notice)
	return verdict
}

// shaRefusal returns the bounded native pre-tool refusal for commit resolution failures.
func shaRefusal(reason string) Verdict {
	return Verdict{Outcome: Deny, Reason: boundReason(refusal("HISS", "literal commit SHA check: "+reason))}
}

// judgeSHAStatement tracks shell directories and resolves only operands of scoped commands.
func judgeSHAStatement(ctx context.Context, words []string, base string) (string, string) {
	word, args, dir, _, _ := walkCommandWords(words, base)
	name := commandName(word)
	if name == "cd" || name == "pushd" {
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 0 {
			return shaRepositoryDirectory(args[0], dir), ""
		}
		return "", ""
	}
	if name == "git" {
		args, dir = gitSHAArguments(args, dir)
	}
	for _, operand := range commandSHAOperands(name, args) {
		if reason := resolveSHAOperand(ctx, dir, operand); reason != "" {
			return base, reason
		}
	}
	return base, ""
}

// shaRepositoryDirectory retains missing paths so Git can report the failing repository.
// Dynamic directory operands remain visibly unresolved rather than falling back to cwd.
func shaRepositoryDirectory(raw, base string) string {
	value := homeTarget(unquoteShellWord(raw))
	if value == "" || hasUnresolvedTargetSyntax(value) {
		return value
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, value)
}

// gitSHAArguments consumes Git global options and applies successive -C directories.
func gitSHAArguments(args []string, dir string) ([]string, string) {
	for i := 0; i < len(args); i++ {
		option, value, next := directoryOption(args, i)
		if !strings.HasPrefix(option, "-") {
			return args[i:], dir
		}
		if option == "-C" && unquoteTarget(value) != "" {
			dir = shaRepositoryDirectory(value, dir)
		}
		i = next
	}
	return nil, dir
}

// resolveSHAOperand uses the bounded, isolated Git reader; no fetch or shell evaluation.
func resolveSHAOperand(ctx context.Context, dir string, operand commitSHAOperand) string {
	label := fmt.Sprintf("%s value %q; repository %q", operand.flag, operand.value, dir)
	if dir == "" || hasUnresolvedTargetSyntax(dir) {
		return label + "; repository directory unresolved"
	}
	result, err := util.RunGitProbeWithin(ctx, dir, 4096, 3*time.Second, "cat-file", "-e", operand.value+"^{commit}")
	if err != nil {
		reason := label + "; local commit resolution failed: " + err.Error()
		if diagnostic := strings.TrimSpace(string(result.Stderr)); diagnostic != "" {
			reason += "; " + diagnostic
		}
		return reason
	}
	return ""
}
