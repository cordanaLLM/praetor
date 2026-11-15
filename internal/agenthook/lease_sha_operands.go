// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import "strings"

// commandSHAOperands extracts scoped write guards and full-length read-filter literals.
func commandSHAOperands(name string, args []string) []commitSHAOperand {
	if name == "bash" || name == "sh" {
		if script, next, ok := nextGHPositional(args, 0); ok {
			name, args = commandName(script), args[next:]
		}
	}
	if name == "squash_merge.sh" && len(args) >= 2 {
		return appendSHAOperand(nil, args[1], "squash_merge.sh head SHA", 7)
	}
	if name == "git" && len(args) > 0 {
		return gitSHAOperands(unquoteTarget(args[0]), args[1:])
	}
	if name == "gh" {
		return ghSHAOperands(args)
	}
	return nil
}

// appendSHAOperand accepts a whole hex word only; expansions and substitutions stay runtime data.
func appendSHAOperand(out []commitSHAOperand, raw, flag string, minLength int) []commitSHAOperand {
	value := unquoteShellWord(raw)
	if len(value) >= minLength && literalCommitSHA.MatchString(value) {
		return append(out, commitSHAOperand{value: value, flag: flag})
	}
	return out
}

// gitSHAOperands checks leases and positional full SHA-1 names before the path separator.
func gitSHAOperands(subcommand string, args []string) []commitSHAOperand {
	if subcommand == "push" {
		return flagSHAOperands(args, "--force-with-lease", 7, true)
	}
	if subcommand != "log" && subcommand != "show" {
		return nil
	}
	var out []commitSHAOperand
	for _, arg := range args {
		if arg == "--" {
			break
		}
		out = appendSHAOperand(out, arg, "git "+subcommand, 40)
	}
	return out
}

// flagSHAOperands consumes both equals and space forms without interpreting a lease ref as a SHA.
func flagSHAOperands(args []string, flag string, minLength int, lease bool) []commitSHAOperand {
	var out []commitSHAOperand
	for i := 0; i < len(args); i++ {
		value, next, matched, present := flagValue(args, i, "", flag)
		if !matched || !present {
			continue
		}
		i = next
		value = unquoteShellWord(value)
		if lease {
			var found bool
			_, value, found = strings.Cut(value, ":")
			if !found {
				continue
			}
		}
		out = appendSHAOperand(out, value, flag, minLength)
	}
	return out
}

// ghSHAOperands identifies merge guards, run filters and commit names embedded in API paths.
func ghSHAOperands(args []string) []commitSHAOperand {
	subcommand, next, ok := nextGHPositional(args, 0)
	if !ok {
		return nil
	}
	if subcommand == "api" {
		return apiArgumentSHAs(args[next:])
	}
	action, after, ok := nextGHPositional(args, next)
	if !ok {
		return nil
	}
	switch subcommand + " " + action {
	case "pr merge":
		return flagSHAOperands(args[after:], "--match-head-commit", 7, false)
	case "run list":
		return flagSHAOperands(args[after:], "--commit", 40, false)
	default:
		return nil
	}
}

// apiArgumentSHAs skips API flag values so headers and field data cannot masquerade as endpoints.
func apiArgumentSHAs(args []string) []commitSHAOperand {
	var out []commitSHAOperand
	for i := 0; i < len(args); i++ {
		tok := unquoteTarget(args[i])
		if _, next, matched, _ := flagValue(args, i, "-X", "-X", "--method"); matched {
			i = next
			continue
		}
		if next, matched := parseGHAPIFieldFlag(args, i, tok); matched {
			i = next
			continue
		}
		if ghAPIValueFlags[tok] {
			i++
			continue
		}
		if !strings.HasPrefix(tok, "-") {
			out = append(out, apiSHAOperands(tok)...)
		}
	}
	return out
}

// apiSHAOperands extracts complete SHA-1 path components, including compare endpoints.
func apiSHAOperands(path string) []commitSHAOperand {
	var out []commitSHAOperand
	path, _, _ = strings.Cut(unquoteShellWord(path), "?")
	for _, part := range strings.FieldsFunc(path, func(c rune) bool { return c == '/' || c == '.' }) {
		out = appendSHAOperand(out, part, "gh api commit path", 40)
	}
	return out
}
