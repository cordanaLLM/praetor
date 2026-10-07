package forge

import (
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"gopkg.in/yaml.v3"
)

// How MeasureBuildWarnings reads one run: script: its fields (scriptFields) are cut into
// commands at the separators (commandSeparators), each command's fields are joined back into
// shell words across a quoted value, and each command is read as its assignments, its program
// and its arguments. A wrapper or a reserved word before the program (commandPrefixes) starts no
// program of its own. Only POSIX shell syntax is read: a PowerShell or cmd assignment is not.

// commandPrefixes are the words before a command's program that run no program of their own:
// the reserved words that open a compound command, and the wrappers that run the program named
// after them.
var commandPrefixes = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "do": true, "while": true, "until": true,
	"!": true, "{": true, "time": true, "sudo": true, "env": true, "nice": true, "nohup": true,
	"command": true, "exec": true, "ccache": true, "sccache": true,
}

var (
	// shellAssignmentWord is a variable assignment word, NAME=value, once its quotes are removed.
	shellAssignmentWord = regexp.MustCompile(`(?s)^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	// variableReference is a word that is one whole variable reference, $NAME or ${NAME}.
	variableReference = regexp.MustCompile(`^\$(?:([A-Za-z_][A-Za-z0-9_]*)|\{([A-Za-z_][A-Za-z0-9_]*)\})$`)
	// unquoter removes the quote characters from a word.
	unquoter = strings.NewReplacer(`"`, "", `'`, "")
)

// scriptCommands cuts a run: script's fields into its commands, each as its shell words.
func scriptCommands(fields []string) [][]string {
	var commands [][]string
	start := 0
	for i := 0; i <= len(fields) && i <= maxRunScriptFields; i++ {
		if i < len(fields) && !commandSeparators[fields[i]] {
			continue
		}
		if i > start {
			commands = append(commands, shellWords(fields[start:i]))
		}
		start = i + 1
	}
	return commands
}

// shellWords joins one command's whitespace fields back into its words: a field that opens a
// quote it does not close is joined, with one space, to the fields up to the one closing it.
// The quote characters are then removed, so `RUSTFLAGS="-D warnings"` reads as the one word
// RUSTFLAGS=-D warnings.
func shellWords(fields []string) []string {
	words := make([]string, 0, len(fields))
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		word := fields[i]
		for openQuote(word) != 0 && i+1 < len(fields) {
			i++
			word += " " + fields[i]
		}
		words = append(words, unquoter.Replace(word))
	}
	return words
}

// openQuote returns the quote character word leaves open, or 0. A backslash outside single
// quotes escapes the character after it.
func openQuote(word string) byte {
	var open byte
	for i := 0; i < len(word); i++ {
		switch c := word[i]; {
		case open == 0 && (c == '"' || c == '\''):
			open = c
		case c == open:
			open = 0
		case c == '\\' && open != '\'':
			i++
		}
	}
	return open
}

// shellCommand is one command: the assignments before its program, the program's base name,
// lower-cased and without .exe, and its arguments.
type shellCommand struct {
	assigns map[string]string
	program string
	args    []string
}

// parseShellCommand reads one command's words.
func parseShellCommand(words []string) shellCommand {
	var cmd shellCommand
	i := 0
	for ; i < len(words) && i < maxRunScriptFields; i++ {
		if commandPrefixes[words[i]] {
			continue
		}
		match := shellAssignmentWord.FindStringSubmatch(words[i])
		if match == nil {
			break
		}
		if cmd.assigns == nil {
			cmd.assigns = map[string]string{}
		}
		cmd.assigns[match[1]] = match[2]
	}
	if i < len(words) {
		cmd.program = strings.TrimSuffix(strings.ToLower(commandName(words[i])), ".exe")
		cmd.args = words[i+1:]
	}
	return cmd
}

// stepEnvironment is the environment the commands of one step read: what the script itself
// exported so far, over the step's, job's and workflow's env: (scopes, innermost first).
type stepEnvironment struct {
	scopes []*yaml.Node
	script map[string]string
}

// value returns name's value for a command with the prefix assignments assigns: set is false
// when nothing sets it, known false when an env: expression decides it (ghworkflow.EnvValue).
func (e stepEnvironment) value(name string, assigns map[string]string) (value string, set, known bool) {
	if value, ok := assigns[name]; ok {
		return value, true, true
	}
	if value, ok := e.script[name]; ok {
		return value, true, true
	}
	return ghworkflow.EnvValue(name, e.scopes...)
}

// assign records assignments the script makes for its later commands: an exported one always,
// and a plain one only for a variable already in the environment, the one case in which a
// program the script starts later sees it.
func (e stepEnvironment) assign(assigns map[string]string, exported bool) {
	for name, value := range assigns {
		if _, set, _ := e.value(name, nil); exported || set {
			e.script[name] = value
		}
	}
}

// expand replaces each argument that is one whole variable reference to a variable the
// environment sets with the fields of its value. The command's own assignments do not apply:
// the shell expands its words before it assigns them.
func (e stepEnvironment) expand(args []string) []string {
	expanded := make([]string, 0, len(args))
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		match := variableReference.FindStringSubmatch(args[i])
		if match == nil {
			expanded = append(expanded, args[i])
			continue
		}
		value, set, known := e.value(match[1]+match[2], nil)
		if !set || !known {
			expanded = append(expanded, args[i])
			continue
		}
		expanded = append(expanded, strings.Fields(value)...)
	}
	return expanded
}

// jobKey names the job of site for one toolchain, the key of buildMeasure.configured.
func (site *laneSite) jobKey(toolchain string) string {
	return site.lane.Workflow + "/" + site.lane.Job + "/" + toolchain
}
