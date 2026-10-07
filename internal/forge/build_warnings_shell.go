package forge

import (
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"gopkg.in/yaml.v3"
)

// How MeasureBuildWarnings reads one run: script: its fields (scriptFields) are cut into
// commands at the separators (commandSeparators), each command's fields are joined back into
// shell words across a quoted value or a ${{ }} expression, the parentheses of a subshell are
// dropped, and each command is read as its assignments, its program and its arguments. A
// reserved word or a wrapper before the program (commandPrefixes, commandWrappers) starts no
// program of its own. Only POSIX shell syntax is read: a PowerShell or cmd assignment is not.

// commandPrefixes are the words before a command's program that run no program of their own and
// take no option: the reserved words that open a compound command, and the wrappers that run the
// program named right after them. The "(" that opens a subshell is cut from the word it starts
// (shellCommand.prefix).
var commandPrefixes = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "do": true, "while": true, "until": true,
	"!": true, "{": true, "nohup": true, "command": true, "ccache": true, "sccache": true,
}

// wrapperSpec is how a wrapper that runs the program named after it reads its own words: the
// options whose value is the next word, and the operands between its options and the program.
type wrapperSpec struct {
	valueOptions map[string]bool
	operands     int
}

// commandWrappers are the wrappers with options of their own, each read as its manual page
// documents it: sudo(8), env(1), nice(1), time(1), exec (bash(1)) and timeout(1), whose one
// operand is the duration.
var commandWrappers = map[string]wrapperSpec{
	"sudo": {valueOptions: addNames(nil, []string{"-C", "--close-from", "-D", "--chdir", "-g", "--group", "-h", "--host",
		"-p", "--prompt", "-R", "--chroot", "-T", "--command-timeout", "-U", "--other-user", "-u", "--user"})},
	"env":     {valueOptions: addNames(nil, []string{"-u", "--unset", "-C", "--chdir", "-S", "--split-string", "-a", "--argv0"})},
	"nice":    {valueOptions: addNames(nil, []string{"-n", "--adjustment"})},
	"time":    {valueOptions: addNames(nil, []string{"-f", "--format", "-o", "--output"})},
	"exec":    {valueOptions: addNames(nil, []string{"-a"})},
	"timeout": {valueOptions: addNames(nil, []string{"-s", "--signal", "-k", "--kill-after"}), operands: 1},
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
		if words := subshellWords(shellWords(fields[start:i])); len(words) > 0 {
			commands = append(commands, words)
		}
		start = i + 1
	}
	return commands
}

// subshellWords drops the parentheses of a subshell from one command's words: the "(" its first
// word opens with, unless it opens a $( ) substitution, and each ")" its last word closes that
// the word itself does not open, so the cmake .. -DX=ON) of (cd build && cmake .. -DX=ON) reads
// as cmake .. -DX=ON while a balanced $(nproc) is kept. A last word left empty is dropped. A "("
// after a reserved word, as in then (cmake ...), is cut where the words are read
// (shellCommand.prefix).
func subshellWords(words []string) []string {
	if len(words) == 0 {
		return words
	}
	if !strings.HasPrefix(words[0], "$(") {
		words[0] = strings.TrimLeft(words[0], "(")
	}
	last := words[len(words)-1]
	for unbalanced := strings.Count(last, ")") - strings.Count(last, "("); unbalanced > 0; unbalanced-- {
		last = strings.TrimSuffix(last, ")")
	}
	words[len(words)-1] = last
	return slices.DeleteFunc(words, func(word string) bool { return word == "" })
}

// shellWords joins one command's whitespace fields back into its words: a field that opens a
// quote, or a ${{ }} expression, it does not close is joined, with one space, to the fields up to
// the one closing it. The quote characters are then removed, so `RUSTFLAGS="-D warnings"` reads
// as the one word RUSTFLAGS=-D warnings, and ${{ matrix.cc }} as one word.
func shellWords(fields []string) []string {
	words := make([]string, 0, len(fields))
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		word := fields[i]
		for (openQuote(word) != 0 || openExpression(word)) && i+1 < len(fields) {
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

// openExpression reports whether word opens a ${{ }} expression it does not close.
func openExpression(word string) bool {
	return strings.LastIndex(word, "${{") > strings.LastIndex(word, "}}")
}

// shellCommand is one command: the assignments after its last wrapper (assigns), those before it
// (outer, which the wrapper inherits as its environment), what of the environment the wrappers
// let reach the program (reach), the program's word as written (word), its base name,
// lower-cased and without .exe (program), and its arguments.
type shellCommand struct {
	assigns, outer map[string]string
	reach          envReach
	word, program  string
	args           []string
}

// parseShellCommand reads one command's words.
func parseShellCommand(words []string) shellCommand {
	var cmd shellCommand
	i := 0
	for i < len(words) && i < maxRunScriptFields {
		next, prefix := cmd.prefix(words, i)
		if !prefix {
			break
		}
		i = next
	}
	if i < len(words) {
		cmd.word, cmd.program, cmd.args = words[i], programName(words[i]), words[i+1:]
	}
	return cmd
}

// prefix reads words[i] when it comes before the program: a reserved word, a wrapper with its
// options, or an assignment. It returns the index after it, and false at the program. The "("
// that opens a subshell is cut from the word it starts, unless it opens a $( ) substitution.
func (cmd *shellCommand) prefix(words []string, i int) (int, bool) {
	word := words[i]
	if trimmed := strings.TrimLeft(word, "("); trimmed != word && !strings.HasPrefix(word, "$(") {
		if trimmed == "" {
			return i + 1, true
		}
		words[i], word = trimmed, trimmed
	}
	if commandPrefixes[word] {
		return i + 1, true
	}
	if spec, ok := commandWrappers[word]; ok {
		return cmd.wrapper(word, spec, words, i+1), true
	}
	match := shellAssignmentWord.FindStringSubmatch(word)
	if match == nil {
		return i, false
	}
	if cmd.assigns == nil {
		cmd.assigns = map[string]string{}
	}
	cmd.assigns[match[1]] = match[2]
	return i + 1, true
}

// wrapper reads the options and operands of the wrapper name from words[i] on, recording what
// they do to the environment, and returns the index after them. The assignments before the
// wrapper become the environment it inherits. sudo resets that environment by default (sudoers(5)
// env_reset, on by default) unless -E or --preserve-env keeps it.
func (cmd *shellCommand) wrapper(name string, spec wrapperSpec, words []string, i int) int {
	cmd.inherit()
	if name == "sudo" {
		cmd.reach.reset = true
	}
	for ; i < len(words) && i < maxRunScriptFields; i++ {
		option := words[i]
		if option == "--" {
			i++
			break
		}
		if !strings.HasPrefix(option, "-") {
			break
		}
		value := ""
		if spec.valueOptions[option] && i+1 < len(words) {
			i++
			value = words[i]
		}
		cmd.reach.option(name, option, value)
	}
	return min(i+spec.operands, len(words))
}

// inherit moves the command's assignments so far into the environment a wrapper inherits.
func (cmd *shellCommand) inherit() {
	if len(cmd.assigns) == 0 {
		return
	}
	if cmd.outer == nil {
		cmd.outer = map[string]string{}
	}
	for name, value := range cmd.assigns {
		cmd.outer[name] = value
	}
	cmd.assigns = nil
}

// envReach is what of the step's environment reaches a program through its wrappers: a reset
// environment keeps only the kept names, and dropped names never arrive.
type envReach struct {
	reset         bool
	kept, dropped map[string]bool
}

// sudoValueLetters are the short sudo options that take a value, which a cluster such as -uroot
// carries attached.
const sudoValueLetters = "CDghpRTUu"

// option records what one wrapper option does to the environment (sudoOption, envOption).
func (r *envReach) option(wrapper, option, value string) {
	switch wrapper {
	case "sudo":
		r.sudoOption(option)
	case "env":
		r.envOption(option, value)
	}
}

// sudoOption records a sudo option: -E (alone or in a cluster such as -nE) and --preserve-env
// keep the whole environment, --preserve-env=list the listed names (sudo(8)).
func (r *envReach) sudoOption(option string) {
	if list, ok := strings.CutPrefix(option, "--preserve-env="); ok {
		r.kept = addNames(r.kept, strings.Split(list, ","))
		return
	}
	if option == "--preserve-env" || shortCluster(option, 'E', sudoValueLetters) {
		r.reset = false
	}
}

// envOption records an env option: -i, - and --ignore-environment start from an empty
// environment, and -u NAME, --unset NAME and --unset=NAME remove one name (env(1)).
func (r *envReach) envOption(option, value string) {
	if name, ok := strings.CutPrefix(option, "--unset="); ok {
		option, value = "--unset", name
	}
	switch option {
	case "-i", "-", "--ignore-environment":
		r.reset = true
	case "-u", "--unset":
		r.dropped = addNames(r.dropped, []string{value})
	}
}

// shortCluster reports whether option is a cluster of short options, such as -nE, holding
// letter, with no value attached to a leading option of valueLetters.
func shortCluster(option string, letter rune, valueLetters string) bool {
	if len(option) < 2 || option[0] != '-' || option[1] == '-' || strings.ContainsRune(valueLetters, rune(option[1])) {
		return false
	}
	return strings.ContainsRune(option[1:], letter)
}

// addNames returns set with names added.
func addNames(set map[string]bool, names []string) map[string]bool {
	if set == nil {
		set = map[string]bool{}
	}
	for i := 0; i < len(names); i++ {
		set[names[i]] = true
	}
	return set
}

// hides reports whether the environment variable name does not reach the program.
func (r envReach) hides(name string) bool {
	return r.dropped[name] || r.reset && !r.kept[name]
}

// programName is the base name of a command's program word, lower-cased and without .exe. The
// base name is cut at a slash or a backslash on every host (commandName), so a workflow reads the
// same wherever the audit runs (HISS-21).
func programName(word string) string {
	return strings.TrimSuffix(strings.ToLower(commandName(word)), ".exe")
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

// resolveProgram reads a program given as one variable reference ($CC, ${CC}) the way the shell
// expands it: the value's words, read again as a command, give the program and lead its
// arguments. A program word holding any other expansion or a ${{ }} expression, or naming a
// variable the step does not set or sets through an expression, is unread: resolveProgram
// reports false and the command stays as written.
func (e stepEnvironment) resolveProgram(cmd shellCommand) (shellCommand, bool) {
	if !strings.ContainsAny(cmd.word, "$`") {
		return cmd, true
	}
	match := variableReference.FindStringSubmatch(cmd.word)
	if match == nil {
		return cmd, false
	}
	value, set, known := e.value(match[1]+match[2], nil)
	fields := strings.Fields(value)
	if !set || !known || len(fields) == 0 || strings.ContainsAny(value, "$`") {
		return cmd, false
	}
	resolved := parseShellCommand(append(fields, cmd.args...))
	if resolved.program == "" || strings.ContainsAny(resolved.word, "$`") {
		return cmd, false
	}
	cmd.word, cmd.program, cmd.args = resolved.word, resolved.program, resolved.args
	return cmd, true
}

// names returns, sorted, the names cmd's program reads a value for (lookup) that start with
// prefix and end with something more and suffix, and false when an env: expression may set
// others.
func (e stepEnvironment) names(cmd shellCommand, prefix, suffix string) ([]string, bool) {
	declared, known := ghworkflow.EnvNames(e.scopes...)
	for _, set := range []map[string]string{cmd.assigns, cmd.outer, e.script} {
		for name := range set {
			declared = append(declared, name)
		}
	}
	lookup := e.lookup(cmd)
	var names []string
	for i := 0; i < len(declared) && i < maxRunScriptFields; i++ {
		name := declared[i]
		if len(name) <= len(prefix)+len(suffix) || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		if _, set, _ := lookup(name); set {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), known
}

// lookupFunc returns one variable's value as a program reads it: whether it is set, and false
// for known when an env: expression decides it.
type lookupFunc func(name string) (value string, set, known bool)

// lookup is how cmd's program reads a variable: its own assignments after its last wrapper,
// then, unless a wrapper hides the name, the assignments before the wrapper and the step's
// environment.
func (e stepEnvironment) lookup(cmd shellCommand) lookupFunc {
	return func(name string) (string, bool, bool) {
		if value, ok := cmd.assigns[name]; ok {
			return value, true, true
		}
		if cmd.reach.hides(name) {
			return "", false, true
		}
		return e.value(name, cmd.outer)
	}
}

// jobKey names the job of site for one toolchain, the key of buildMeasure.configured.
func (site *laneSite) jobKey(toolchain string) string {
	return site.lane.Workflow + "/" + site.lane.Job + "/" + toolchain
}
