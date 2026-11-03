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
	// admission.ScopeArgs emits ten options including the final --; its fixture
	// is the longest real run. Keep it usable while bounding ambiguous prefixes.
	privilegeOptionLimit = 10
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

var privilegeOptionOverflowSource = privilegeOptionOverflowRule()

var privilegeInvocationPattern = regexp.MustCompile(`^` + privilegeInvocation)

// shellProgram matches a command name, optionally reached through a filesystem path.
func shellProgram(name string) string {
	return `(?:[^ \t\n;&|()\x60\x22\x27]*/)?(?:` + name + `)`
}

// privilegeCommandRule requires command position, unlike lineThroughFirstWord,
// whose whole-word search intentionally includes data. Launcher option arities
// come from admissionWrappers, so the text and act policies share that knowledge.
func privilegeCommandRule() string {
	return privilegeCommandPrefix() + privilegeInvocation
}

// privilegeOptionRun bounds alternatives that can read a prefix as flag or value option.
func privilegeOptionRun(values []string, minimum, maximum int) string {
	return fmt.Sprintf(`(?:%s(?:%s)){%d,%d}`, shellBlank, privilegeOptions(values), minimum, maximum)
}

// privilegeCommandPrefix reaches command positions through bounded wrapper option runs.
func privilegeCommandPrefix() string {
	names := make([]string, 0, len(admissionWrappers))
	for name := range admissionWrappers {
		names = append(names, name)
	}
	slices.Sort(names)
	launchers := make([]string, 0, len(names))
	for _, name := range names {
		spec := admissionWrappers[name]
		launcher := shellProgram(name) + privilegeOptionRun(spec.values, 0, privilegeOptionLimit) + shellBlank
		launcher += privilegePositionals(spec)
		launchers = append(launchers, launcher)
	}
	assignment := `[A-Za-z_][A-Za-z0-9_]*=(?:` + privilegeWord + `)?` + shellBlank
	position := `(?:\A|[\n;&|({!\x60]|\x22` + privilegeDoubleText + `(?:\$\(|\x60))`
	return `\A` + privilegeData + position + `[ \t]*(?:` + assignment + `|` + strings.Join(launchers, `|`) + `|` + privilegeScriptPrefix() + `|(?:then|do|else|if|while|until)` + shellBlank + `)*`
}

// privilegeOptionOverflowRule refuses excess options independently of protected targets.
// The named group puts the bound at the front of even a truncated refusal reason.
func privilegeOptionOverflowRule() string {
	values := make(map[string][]string, len(admissionWrappers)+len(privilegeLauncherValues))
	for name, spec := range admissionWrappers {
		values[name] = spec.values
	}
	for name, options := range privilegeLauncherValues {
		values[name] = options
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names)+1)
	for _, name := range names {
		parts = append(parts, quotedPrivilegeProgram(name)+privilegeOptionRun(values[name], privilegeOptionLimit+1, privilegeOptionLimit+1))
	}
	// su accepts a username among its options; bound that word run as well.
	parts = append(parts, quotedPrivilegeProgram("su")+fmt.Sprintf(`(?:%s%s){%d}`, shellBlank, privilegeWord, privilegeOptionLimit+1))
	return fmt.Sprintf(`(?P<option_run_exceeds_%d>`, privilegeOptionLimit) + privilegeCommandPrefix() + `(?:` + strings.Join(parts, `|`) + `))`
}

// privilegePositionals shares the table's optional numeric priority grammar.
func privilegePositionals(spec wrapperSpec) string {
	if spec.optionalPriority {
		return `(?:[0-9]+` + shellBlank + `)?`
	}
	return strings.Repeat(privilegeWord+shellBlank, spec.positional)
}

// privilegeShortValues lists the short options whose first occurrence consumes the
// rest of its cluster, or the next word when no attached value remains.
func privilegeShortValues(values []string) string {
	var letters strings.Builder
	for _, value := range values {
		if len(value) == 2 && value[0] == '-' {
			letters.WriteByte(value[1])
		}
	}
	return letters.String()
}

// privilegeOptions also reads long value-option prefixes as taking a value.
// Prefixes retain the flag reading: getopt_long may resolve them to a valueless
// option instead. Exact value options stay disjoint to avoid treating data as code.
func privilegeOptions(values []string) string {
	longValues := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, "--") {
			longValues = append(longValues, value[1:])
		}
	}
	long := `--|-` + privilegeFlagExcept(longValues)
	if len(longValues) > 0 {
		prefixes := privilegeLongPrefixes(longValues)
		long = `(?:-` + strings.Join(prefixes, `|-`) + `)` + shellBlank + privilegeValue + `|` + long
	}
	letters := ""
	for _, letter := range privilegeShortValues(values) {
		letters += fmt.Sprintf(`\x%02x`, letter)
	}
	plain := `[^` + privilegeOptionChar + `\x2d` + letters + `]`
	short := `-` + plain + `+`
	if letters != "" {
		short += `|-` + plain + `*[` + letters + `](?:` + shellBlank + privilegeValue + `|[^` + privilegeOptionChar + `]+)`
	}
	return long + `|` + short
}

// privilegeLongPrefixes includes every nonempty long-name prefix, failing closed
// even when several value options share a prefix. Hex literals keep abbreviations
// and option-letter clusters from appearing as misspelled words in emitted text.
func privilegeLongPrefixes(values []string) []string {
	var prefixes []string
	for _, value := range values {
		encoded := "-"
		for index := 1; index < len(value); index++ {
			encoded += fmt.Sprintf(`\x%02x`, value[index])
			prefixes = append(prefixes, encoded)
		}
	}
	slices.Sort(prefixes)
	return slices.Compact(prefixes)
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
		parts = append(parts, quotedPrivilegeProgram(name)+privilegeOptionRun(values, 0, privilegeOptionLimit)+shellBlank+script)
	}
	su := quotedPrivilegeProgram("su") + fmt.Sprintf(`(?:%s%s){0,%d}`, shellBlank, privilegeWord, privilegeOptionLimit) + shellBlank
	command := `(?:-[A-Za-z]*c[A-Za-z]*[ \t]*|--command(?:=|` + shellBlank + `))`
	return `(?:` + strings.Join(parts, `|`) + `|` + su + command + script + `)`
}
