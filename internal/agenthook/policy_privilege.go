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
	// Values are quoted tokens, non-options or negative integers. The flag trie
	// below prevents value-taking flags from also parsing as valueless flags.
	privilegeValue      = `(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^- \t\n;&|()\x60\x22\x27][^ \t\n;&|()\x60\x22\x27]*|-[0-9]+)`
	privilegeOptionChar = ` \t\n;&|()\x60\x22\x27`
	privilegeEnd        = `(?:[ \t\r\n;&|()\x60]|$)`
	// Consume quoted data as complete atoms before looking for command position.
	// A separate double-quote branch reaches substitutions that actually execute.
	privilegeDoubleText = `(?:[^\x22\\]|\\(?s:.))*`
	privilegeData       = `(?:[^\x22\x27\\]|\\(?s:.)|\x27[^\x27]*\x27|\x22` + privilegeDoubleText + `\x22)*`
)

// privilegeNames also declares the text rule's necessary-literal prefilter. su
// needs a command option; the other names refuse any invocation.
var privilegeNames = []string{"sudo", "doas", "pkexec", "run0", "su"}

// su without a command remains an interactive invocation, outside this rule.
var privilegeInvocation = `(?:(?:` + strings.Join(privilegeNames[:4], `|`) + `)` + privilegeEnd +
	`|su(?:` + shellBlank + privilegeWord + `)*` + shellBlank + `(?:-c[^ \t\n;&|()\x60]*|--command(?:=[^ \t\n;&|()\x60]*)?)` + privilegeEnd + `)`

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
		if spec.positional > 0 {
			launcher += privilegeWord + shellBlank
		}
		launchers = append(launchers, launcher)
	}
	assignment := `[A-Za-z_][A-Za-z0-9_]*=(?:` + privilegeWord + `)?` + shellBlank
	position := `(?:\A|[\n;&|(\x60]|\x22` + privilegeDoubleText + `(?:\$\(|\x60))`
	return `\A` + privilegeData + position + `[ \t]*(?:` + assignment + `|` + strings.Join(launchers, `|`) + `)*` +
		shellProgram(privilegeInvocation)
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
