// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"slices"
	"strconv"
	"strings"
)

// holdWrapperSpec adds an option allowlist to the shared wrapper value/position knowledge.
type holdWrapperSpec struct {
	wrapperSpec
	flags    []string
	optional []string
}

// holdWrapper returns installed env 9.12 and sudo 1.9.17p2 option arities; unknowns fail closed.
func holdWrapper(name string) (holdWrapperSpec, bool) {
	switch name {
	case "env":
		return holdWrapperSpec{wrapperSpec: wrapperSpec{values: []string{
			"-a", "--argv0", "-u", "--unset", "-C", "--chdir", "-S", "--split-string", "--env0-from"}},
			flags:    []string{"-", "-i", "--ignore-environment", "-0", "--null", "-v", "--debug", "--list-signal-handling", "--help", "--version"},
			optional: []string{"--block-signal", "--default-signal", "--ignore-signal"}}, true
	case "sudo":
		return holdWrapperSpec{wrapperSpec: wrapperSpec{values: []string{
			"-u", "--user", "-g", "--group", "-h", "--host", "-p", "--prompt", "-r", "--role", "-t", "--type",
			"-C", "--close-from", "-D", "--chdir", "-R", "--chroot", "-T", "--command-timeout", "-U", "--other-user",
			"-a", "--auth-type", "-c", "--login-class"}},
			flags: []string{"-A", "--askpass", "-B", "--bell", "-b", "--background", "-E", "-e", "--edit", "-H", "--set-home",
				"--help", "-i", "--login", "-K", "--remove-timestamp", "-k", "--reset-timestamp", "-l", "--list", "-N", "--no-update",
				"-n", "--non-interactive", "-P", "--preserve-groups", "-S", "--stdin", "-s", "--shell", "-V", "--version", "-v", "--validate"},
			optional: []string{"--preserve-env"}}, true
	}
	spec, ok := admissionWrappers[name]
	return holdWrapperSpec{wrapperSpec: spec, flags: holdWrapperFlags[name]}, ok
}

// holdWrapperFlags permits known no-value options without guessing unknown option arities.
var holdWrapperFlags = map[string][]string{
	"command": {"-p", "-v", "-V"}, "builtin": {}, "exec": {"-c", "-l"},
	"nice": {"--help", "--version"}, "nohup": {"--help", "--version"},
	"timeout":     {"--preserve-status", "--foreground", "-v", "--verbose", "--help", "--version"},
	"setsid":      {"-c", "--ctty", "-f", "--fork", "-w", "--wait"},
	"systemd-run": {"--user", "--scope", "--wait", "--collect", "--quiet", "--pipe", "--pty"},
	"flock":       {"-s", "--shared", "-x", "--exclusive", "-u", "--unlock", "-n", "--nb", "--nonblocking", "-F", "--no-fork", "-o", "--close"},
	"time":        {"-p"},
	"taskset":     {"-a", "--all-tasks", "-p", "--pid", "-c", "--cpu-list"},
	"chrt":        {"-a", "--all-tasks", "-b", "--batch", "-f", "--fifo", "-i", "--idle", "-o", "--other", "-r", "--rr", "-p", "--pid"},
	"ionice":      {"-t", "--ignore"}, "xargs": {"-0", "--null", "-r", "--no-run-if-empty", "-t", "--verbose", "-p", "--interactive", "-x", "--exit"},
}

// holdWrapperCursor carries the option terminator and remaining positional operands.
type holdWrapperCursor struct {
	positional  int
	optionsDone bool
}

// holdWrappedCommand finds a wrapper's command while refusing unknown option shapes.
func holdWrappedCommand(name string, words []holdWord, i int) (int, bool, string) {
	spec, ok := holdWrapper(name)
	if !ok {
		return i, false, ""
	}
	cursor := holdWrapperCursor{positional: spec.positional}
	for j := i + 1; j < len(words) && j < admissionMaxWords; {
		next, done, reason := holdWrapperStep(name, words, j, spec, &cursor)
		if done || reason != "" {
			return next, true, reason
		}
		j = next
	}
	return len(words), true, ""
}

// holdWrapperStep consumes one known option or operand, or identifies the command word.
func holdWrapperStep(name string, words []holdWord, j int, spec holdWrapperSpec, cursor *holdWrapperCursor) (int, bool, string) {
	word := words[j]
	if !word.literal {
		return j, false, "cannot judge wrapper word " + word.raw
	}
	if !cursor.optionsDone && word.value == "--" {
		cursor.optionsDone = true
		return j + 1, false, ""
	}
	if !cursor.optionsDone && strings.HasPrefix(word.value, "-") {
		next, reason := holdConsumeOption(name, words, j, spec)
		return next, false, reason
	}
	if assignmentWord.MatchString(word.value) {
		return j + 1, false, ""
	}
	if cursor.positional > 0 {
		return holdWrapperOperand(name, word, j, cursor)
	}
	return j, true, ""
}

// holdWrapperOperand consumes a positional operand, refusing chrt's ambiguous optional priority.
func holdWrapperOperand(name string, word holdWord, j int, cursor *holdWrapperCursor) (int, bool, string) {
	if name == "chrt" {
		if _, err := strconv.Atoi(word.value); err != nil {
			return j, false, "cannot judge optional chrt priority " + word.raw
		}
	}
	cursor.positional--
	return j + 1, false, ""
}

// holdConsumeOption consumes a validated option and its required literal value.
func holdConsumeOption(name string, words []holdWord, j int, spec holdWrapperSpec) (int, string) {
	takes, reason := holdWrapperOption(name, words[j].value, spec)
	if reason != "" {
		return j, reason
	}
	if !takes {
		return j + 1, ""
	}
	if j+1 >= len(words) {
		return j, "cannot judge missing wrapper value for " + words[j].raw
	}
	if !words[j+1].literal {
		return j, "cannot judge wrapper value " + words[j+1].raw
	}
	return j + 2, ""
}

// holdWrapperOption validates long options or delegates short-cluster arity checking.
func holdWrapperOption(name, word string, spec holdWrapperSpec) (bool, string) {
	if holdTransformOption(name, word) {
		return false, "cannot judge transforming wrapper option " + word
	}
	option, _, attached := strings.Cut(word, "=")
	if slices.Contains(spec.values, option) {
		return !attached, ""
	}
	if slices.Contains(spec.optional, option) {
		return false, ""
	}
	if !attached && slices.Contains(spec.flags, option) {
		return false, ""
	}
	if strings.HasPrefix(word, "--") || len(word) < 2 {
		return false, "cannot judge unknown wrapper option " + word
	}
	return holdShortOptions(name, word, spec)
}

// holdShortOptions checks every short option in a cluster, stopping at a value-taking option.
func holdShortOptions(name, word string, spec holdWrapperSpec) (bool, string) {
	for at := 1; at < len(word); at++ {
		short := "-" + word[at:at+1]
		if holdTransformOption(name, short) {
			return false, "cannot judge transforming wrapper option " + word
		}
		if slices.Contains(spec.values, short) {
			return at == len(word)-1, ""
		}
		if !slices.Contains(spec.flags, short) {
			return false, "cannot judge unknown wrapper option " + word
		}
	}
	return false, ""
}

// holdTransformOption identifies options that reinterpret arguments instead of executing argv.
func holdTransformOption(name, option string) bool {
	key, _, _ := strings.Cut(option, "=")
	if name == "env" {
		return key == "--split-string" || strings.HasPrefix(key, "-S")
	}
	if name == "sudo" {
		return slices.Contains([]string{"-s", "--shell", "-i", "--login", "-e", "--edit"}, key)
	}
	if name == "xargs" {
		return slices.Contains([]string{"-e", "-i", "-l"}, key)
	}
	return name == "flock" && (key == "-c" || key == "--command")
}
