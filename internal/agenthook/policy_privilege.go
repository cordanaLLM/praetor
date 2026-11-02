// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	shellBlank = `[ \t]+`
	// Tokens exclude command boundaries; quoted option values may contain blanks.
	privilegeWord = `(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^ \t\n;&|()\x60\x22\x27]+)`
	// Required option values are complete shell words, including leading dashes.
	// The flag trie keeps value-taking flags disjoint from valueless flags.
	privilegeValue      = privilegeWord
	privilegeOptionChar = ` \t\n;&|()\x60\x22\x27`
	privilegeEnd        = `(?:[ \t\r\n;&|()\x60]|$)`
	// Consume quoted data as complete atoms before looking for command position.
	// A separate double-quote branch reaches substitutions that actually execute.
	privilegeDoubleText = `(?:[^\x22\\]|\\(?s:.))*`
	privilegeData       = `(?:[^\x22\x27\\]|\\(?s:.)|\x27[^\x27]*\x27|\x22` + privilegeDoubleText + `\x22)*`
)

// privilegeNames names launchers whose protected adoption commands are judged.
var privilegeNames = []string{"sudo", "doas", "pkexec", "run0", "su"}

// privilegeLauncherValues lists every option taking a separate value for each privilege
// launcher (sudo(8), doas(1), pkexec(1), and systemd run0(1)).
var privilegeLauncherValues = map[string][]string{
	"sudo": {
		"-a", "--auth-type", "-C", "--close-from", "-c", "--login-class",
		"-D", "--chdir", "-g", "--group", "-h", "--host", "-p", "--prompt",
		"-R", "--chroot", "-r", "--role", "-T", "--command-timeout", "-t", "--type",
		"-U", "--other-user", "-u", "--user",
	},
	"doas":   {"-a", "-C", "-u"},
	"pkexec": {"--user"},
	"run0": {
		"-D", "--chdir", "-g", "--group", "-u", "--user", "--uid", "--gid",
		"--area", "--background", "--description", "--lightweight", "--machine",
		"--nice", "--property", "--setenv", "--shell-prompt-prefix", "--slice", "--unit",
	},
}

var privilegeInvocation = protectedPrivilegeInvocation()

var privilegeCommandSource = privilegeCommandRule()

var privilegeInvocationPattern = regexp.MustCompile(`^` + privilegeInvocation)

// shellProgram matches a command name, optionally reached through a filesystem path.
func shellProgram(name string) string {
	return `(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:` + name + `)`
}

// privilegeCommandRule requires command position, unlike lineThroughFirstWord,
// whose whole-word search intentionally includes data. Launcher option arities
// come from admissionWrappers, so the text and act policies share that knowledge.
func privilegeCommandRule() string {
	names := make([]string, 0, len(admissionWrappers))
	for name := range admissionWrappers {
		names = append(names, name)
	}
	slices.Sort(names)
	launchers := make([]string, 0, len(names))
	for _, name := range names {
		spec := admissionWrappers[name]
		option := privilegeFlagExcept(spec.values)
		if len(spec.values) > 0 {
			option = `(?:` + strings.Join(spec.values, `|`) + `)` + shellBlank + privilegeValue + `|` + option
		}
		launcher := shellProgram(name) + `(?:` + shellBlank + `(?:` + option + `))*` + shellBlank
		launcher += privilegePositionals(spec)
		launchers = append(launchers, launcher)
	}
	assignment := `[A-Za-z_][A-Za-z0-9_]*=(?:` + privilegeWord + `)?` + shellBlank
	position := `(?:\A|[\n;&|({!\x60]|\x22` + privilegeDoubleText + `(?:\$\(|\x60))`
	return `\A` + privilegeData + position + `[ \t]*(?:` + assignment + `|` + strings.Join(launchers, `|`) + `|` + privilegeScriptPrefix() + `|(?:then|do|else|if|while|until)` + shellBlank + `)*` +
		privilegeInvocation
}

// privilegePositionals shares the table's optional numeric priority grammar.
func privilegePositionals(spec wrapperSpec) string {
	if spec.optionalPriority {
		return `(?:[0-9]+` + shellBlank + `)?`
	}
	return strings.Repeat(privilegeWord+shellBlank, spec.positional)
}

// privilegeFlagExcept matches an option token except the exact flags taking a
// separate value. A trie keeps those alternatives disjoint without lookahead
// (unsupported by RE2). Otherwise `env -u env -u ...` has exponentially many
// parses as nested launchers versus option values in Python's re.
func privilegeFlagExcept(values []string) string {
	edges := []map[byte]int{{}}
	terminal := []bool{false}
	for _, value := range values {
		node := 0
		for i := 1; i < len(value); i++ {
			next, found := edges[node][value[i]]
			if !found {
				next = len(edges)
				edges[node][value[i]] = next
				edges = append(edges, map[byte]int{})
				terminal = append(terminal, false)
			}
			node = next
		}
		terminal[node] = true
	}
	return `-` + privilegeFlagTrie(edges, terminal)
}

// privilegeFlagTrie renders leaves before parents: no recursive traversal.
func privilegeFlagTrie(edges []map[byte]int, terminal []bool) string {
	patterns := make([]string, len(edges))
	for node := len(edges) - 1; node >= 0; node-- {
		letters := make([]byte, 0, len(edges[node]))
		for letter := range edges[node] {
			letters = append(letters, letter)
		}
		slices.Sort(letters)
		excluded := privilegeOptionChar
		parts := make([]string, 0, len(letters)+1)
		for _, letter := range letters {
			literal := fmt.Sprintf(`\x%02x`, letter)
			excluded += literal
			parts = append(parts, literal+patterns[edges[node][letter]])
		}
		parts = append(parts, `[^`+excluded+`][^`+privilegeOptionChar+`]*`)
		patterns[node] = `(?:` + strings.Join(parts, `|`) + `)`
		if node > 0 && !terminal[node] {
			patterns[node] += `?`
		}
	}
	return patterns[0]
}

// quotedPrivilegeProgram accepts literal shell quoting and backslash escapes in a name.
func quotedPrivilegeProgram(name string) string {
	var word strings.Builder
	for _, letter := range name {
		word.WriteString(`\\?` + regexp.QuoteMeta(string(letter)))
	}
	quote := `(?:\\?[\x22\x27])?`
	return quote + shellProgram(word.String()) + quote
}

// privilegeScriptPrefix reaches executable script arguments without treating echo data as code.
func privilegeScriptPrefix() string {
	quote := `(?:\\?[\x22\x27])*`
	shell := shellProgram(`(?:ba|da|k|z)?sh`) + shellBlank + `-[A-Za-z]*c[A-Za-z]*` + shellBlank
	return `(?:` + shell + `|eval` + shellBlank + `)` + quote
}

// protectedPrivilegeInvocation restricts privilege checks to adoption at a development root.
func protectedPrivilegeInvocation() string {
	engine := `(?:` + quotedPrivilegeProgram("praetorctl") + `|` + quotedPrivilegeProgram("standardsctl") + `)`
	action := `(?:adopt|conform|bootstrap|needs` + shellBlank + `(?:scan|report|migrate|epic))`
	target := engine + shellBlank + action + privilegeEnd + `[^;\n|]*\bdev[/\\]?(?:[ \t\r\n;&|(){}\x22\x27]|$)`
	script := `(?:` + privilegeScriptPrefix() + `)*` + `(?:\\?[\x22\x27])?` + target
	parts := make([]string, 0, len(privilegeNames))
	for _, name := range privilegeNames[:4] {
		values := privilegeLauncherValues[name]
		option := `(?:` + strings.Join(values, `|`) + `)` + shellBlank + privilegeValue + `|` + privilegeFlagExcept(values)
		parts = append(parts, quotedPrivilegeProgram(name)+`(?:`+shellBlank+`(?:`+option+`))*`+shellBlank+script)
	}
	su := quotedPrivilegeProgram("su") + `(?:` + shellBlank + privilegeWord + `)*` + shellBlank
	command := `(?:-[A-Za-z]*c[A-Za-z]*[ \t]*|--command(?:=|` + shellBlank + `))`
	return `(?:` + strings.Join(parts, `|`) + `|` + su + command + script + `)`
}
