// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	slashpath "path"
	"regexp"
	"strings"
)

// The commands of one logical shell line, and the rules the shell scanner reads from them.
//
// splitShellCommands cuts the lexer's code into simple commands at the control operators (;, &,
// &&, ||, |, |&, and the case item ends ;;, ;& and ;;&) and at the parentheses of subshells and substitutions, and splits each
// command into words at blanks. The rules then read a command's name past the reserved words
// that open a compound command, its assignments and its redirections, and, for the rules about
// what actually runs, past the wrappers that start the program named after them (sudo, env,
// command, exec, nice, nohup, timeout).

const (
	// maxShellCommands bounds the simple commands read from one logical line (HISS-02).
	maxShellCommands = 1024
	// maxShellWrappers bounds the wrappers followed to the program they start (HISS-02).
	maxShellWrappers = 8
)

var (
	// shellAssignment is a variable assignment word: NAME=, NAME+= or NAME[index]=.
	shellAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\[[^\]]*\])?\+?=`)
	// shellRedirection is a redirection word; shellRedirectionOnly is one whose target is the
	// next word.
	shellRedirection     = regexp.MustCompile(`^[0-9]*(?:[<>]|&>)`)
	shellRedirectionOnly = regexp.MustCompile(`^[0-9]*(?:[<>]{1,2}|&>|>&|<&)$`)
)

// shellOperators are the control operators, longest first.
var shellOperators = []string{";;&", "&&", "||", "|&", ";;", ";&", "$(", "<(", ">(", "|", ";", "&", "(", ")"}

// shellCaseEnds are the operators that end a case item: ;; and bash's fall-through ;& and ;;&,
// after each of which the next pattern list starts.
var shellCaseEnds = map[string]bool{";;": true, ";&": true, ";;&": true}

// shellOpeners are the reserved words after which the next word starts a command.
var shellOpeners = map[string]bool{
	"{": true, "}": true, "if": true, "then": true, "elif": true, "else": true,
	"do": true, "while": true, "until": true, "!": true, "time": true,
}

// shellWrappers start the program named after their own options and arguments.
var shellWrappers = map[string]bool{
	"sudo": true, "env": true, "command": true, "builtin": true, "exec": true,
	"nice": true, "nohup": true, "timeout": true,
}

// shellValueOptions are the wrapper options whose value is the next word.
var shellValueOptions = map[string]bool{
	"sudo -u": true, "sudo -g": true, "sudo -h": true, "sudo -p": true, "sudo -C": true,
	"sudo -D": true, "sudo -R": true, "sudo -T": true, "sudo -U": true,
	"env -u": true, "env -C": true, "timeout -k": true, "timeout -s": true, "nice -n": true,
}

// shellInterpreters are the shells that run a script read from their standard input.
var shellInterpreters = map[string]bool{
	"sh": true, "bash": true, "dash": true, "ash": true, "zsh": true, "ksh": true, "mksh": true,
}

// shellCmd is one simple command: the operator before it, its words and their offsets.
type shellCmd struct {
	sep   string
	words []string
	at    []int
}

// splitShellCommands cuts a logical line of code into its simple commands. A process
// substitution stays a word ("<(") of the command it is an argument of, so `source <(...)`
// still names what it sources; the substitution's own code is the next command. An operator
// with no words after it still yields an empty command carrying it, so the close of a case
// pattern list at the end of a line is not lost. Inside a [[ ... ]] test nothing is an
// operator: the regular expression of `[[ $f =~ \.(sh|bash)$ ]]` pipes nothing anywhere.
func splitShellCommands(text string) []shellCmd {
	t := shellTokenizer{text: text, start: -1}
	for i := 0; i < len(text) && len(t.cmds) < maxShellCommands; {
		i = t.step(i)
	}
	t.cur.addWord(text, t.start, len(text))
	return append(t.cmds, t.cur)
}

// shellTokenizer carries splitShellCommands' state through one logical line.
type shellTokenizer struct {
	text string
	cmds []shellCmd
	cur  shellCmd
	// start is where the word being read began, or -1 between words.
	start int
	// test is true inside [[ ... ]].
	test bool
}

// step reads the byte at i and returns the index of the next one to read.
func (t *shellTokenizer) step(i int) int {
	if t.start < 0 {
		if n := t.testBracket(i); n > 0 {
			t.start = i
			return i + n
		}
	}
	if !t.test {
		if op := shellOperatorAt(t.text, i); op != "" {
			return t.operator(i, op)
		}
	}
	if t.text[i] == ' ' || t.text[i] == '\t' {
		t.cur.addWord(t.text, t.start, i)
		t.start = -1
	} else if t.start < 0 {
		t.start = i
	}
	return i + 1
}

// testBracket returns the width of the [[ that opens a test or the ]] that closes it at i,
// toggling the test state, or 0 when neither is there.
func (t *shellTokenizer) testBracket(i int) int {
	word := "[["
	if t.test {
		word = "]]"
	}
	end := i + len(word)
	if !strings.HasPrefix(t.text[i:], word) || (end < len(t.text) && strings.IndexByte(" \t;&|)", t.text[end]) < 0) {
		return 0
	}
	t.test = !t.test
	return len(word)
}

// operator ends the command being read at the operator op at i and starts the next one.
func (t *shellTokenizer) operator(i int, op string) int {
	t.cur.addWord(t.text, t.start, i)
	if op == "<(" || op == ">(" {
		t.cur.addWord(t.text, i, i+2)
	}
	t.cmds = append(t.cmds, t.cur)
	t.cur, t.start = shellCmd{sep: op}, -1
	return i + len(op)
}

// addWord adds text[start:end] as a word when start marks one.
func (c *shellCmd) addWord(text string, start, end int) {
	if start >= 0 && end > start {
		c.words = append(c.words, text[start:end])
		c.at = append(c.at, start)
	}
}

// shellCases follows case statements across logical lines. Inside one, the words before a
// pattern list's closing parenthesis are patterns, which name no command: `*/sh | */bash)` pipes
// nothing into a shell, and a pattern spelled like a function does not call it.
type shellCases struct {
	// depth counts the open case statements.
	depth int
	// pattern is true while a pattern list is being read; awaitIn while a case head waits for
	// its `in` on a later line.
	pattern, awaitIn bool
}

// skip consumes one command and reports whether it is part of a pattern list.
func (c *shellCases) skip(cmd shellCmd) bool {
	first := ""
	if len(cmd.words) > 0 {
		first = cmd.words[0]
	}
	switch {
	case c.depth > 0 && first == "esac":
		c.depth--
		c.pattern = false
	case c.pattern && cmd.sep == ")":
		// The first command of an item may itself open a case: `x) case $b in`.
		c.pattern = false
		c.openCase(cmd.words)
	case c.pattern:
		return true
	case c.depth > 0 && shellCaseEnds[cmd.sep]:
		c.pattern = true
		return true
	case c.awaitIn && first == "in":
		c.awaitIn, c.pattern = false, true
		return true
	default:
		c.openCase(cmd.words)
	}
	return false
}

// openCase starts a case statement when words are its head: case WORD in, with `in` here or on
// a later line, and anything after `in` already a pattern.
func (c *shellCases) openCase(words []string) {
	i := 0
	for i < len(words) && shellOpeners[words[i]] {
		i++
	}
	if i >= len(words) || words[i] != "case" || c.depth >= maxShellOpenFunctions {
		return
	}
	c.depth++
	if i+2 < len(words) && words[i+2] == "in" {
		c.pattern = true
		return
	}
	c.awaitIn = true
}

// shellOperatorAt returns the control operator at i, or "". An & or | that is part of a
// redirection (&>, >&, >|) is not one.
func shellOperatorAt(text string, i int) string {
	for _, op := range shellOperators {
		if !strings.HasPrefix(text[i:], op) {
			continue
		}
		prev := byte(0)
		if i > 0 {
			prev = text[i-1]
		}
		switch {
		case op == "&" && (strings.HasPrefix(text[i+1:], ">") || prev == '>' || prev == '<'):
			return ""
		case op == "|" && prev == '>':
			return ""
		}
		return op
	}
	return ""
}

// commandStart returns the index of the command name in words: past the reserved words that
// open a compound command, the assignments and the redirections. It returns len(words) when the
// command names no program.
func commandStart(words []string) int {
	i := 0
	for i < len(words) {
		switch w := words[i]; {
		case shellOpeners[w], shellAssignment.MatchString(w):
			i++
		case shellRedirectionOnly.MatchString(w):
			i += 2
		case shellRedirection.MatchString(w):
			i++
		default:
			return i
		}
	}
	return len(words)
}

// shellInvocation is the program a command runs once its wrappers are followed.
type shellInvocation struct {
	// name is the program as written; base is its file name.
	name, base string
	args       []string
	// timeout is true when a timeout command bounds the program.
	timeout bool
}

// resolveInvocation follows a command's wrappers to the program they start. `command -v` and
// `command -V` look a name up rather than run it, so they start nothing.
func resolveInvocation(words []string) shellInvocation {
	inv := shellInvocation{}
	i := commandStart(words)
	for n := 0; n < maxShellWrappers && i < len(words); n++ {
		w := words[i]
		if !shellWrappers[w] || (w == "command" && i+1 < len(words) && (words[i+1] == "-v" || words[i+1] == "-V")) {
			break
		}
		inv.timeout = inv.timeout || w == "timeout"
		i = wrapperProgram(words, i)
	}
	if i < len(words) {
		inv.name, inv.base, inv.args = words[i], slashpath.Base(words[i]), words[i+1:]
	}
	return inv
}

// wrapperProgram returns the index of the word after the wrapper at i and its options: the
// option values, env's assignments and the duration timeout takes.
func wrapperProgram(words []string, i int) int {
	wrapper := words[i]
	i++
	for i < len(words) {
		w := words[i]
		switch {
		case shellValueOptions[wrapper+" "+w]:
			i += 2
		case strings.HasPrefix(w, "-"), wrapper == "env" && strings.Contains(w, "="):
			i++
		default:
			if wrapper == "timeout" {
				return i + 1
			}
			return i
		}
	}
	return i
}

// checkCommand applies the per-command rules to one simple command.
func (s *shellScanner) checkCommand(cmd shellCmd) {
	line, _ := s.position(cmd.at[0])
	if isUnboundedShellLoop(cmd.words) {
		recordViolation(s.rep, "HISS-02", s.rel, line, "", "Unbounded loop in shell without an explicit scalar bound")
	}
	if cmd.sep == "||" && len(cmd.words) == 1 && (cmd.words[0] == "true" || cmd.words[0] == ":") {
		recordViolation(s.rep, "HISS-07", s.rel, line, "",
			"'|| "+cmd.words[0]+"' discards the command's failure; handle the exit status or test the condition instead")
	}
	if at := commandStart(cmd.words); at < len(cmd.words) && len(s.sites) < maxShellCommandSites {
		siteLine, col := s.position(cmd.at[at])
		s.sites = append(s.sites, shellSite{name: cmd.words[at], line: siteLine, col: col})
	}
	inv := resolveInvocation(cmd.words)
	if inv.name == "set" {
		s.strict.observe(inv.args)
	}
	s.checkInvocation(cmd.sep, inv, line)
}

// isUnboundedShellLoop reports a while or until loop whose condition is a constant that never
// ends it: while true, while :, until false.
func isUnboundedShellLoop(words []string) bool {
	i := 0
	for i < len(words) && shellOpeners[words[i]] && words[i] != "while" && words[i] != "until" {
		i++
	}
	if len(words) != i+2 {
		return false
	}
	switch words[i] {
	case "while":
		return words[i+1] == "true" || words[i+1] == ":"
	case "until":
		return words[i+1] == "false"
	}
	return false
}

// checkInvocation applies the rules about the program a command runs.
func (s *shellScanner) checkInvocation(sep string, inv shellInvocation, line int) {
	switch {
	case inv.base == "eval":
		recordViolation(s.rep, "HISS-08", s.rel, line, "", "Banned dynamic eval execution in shell")
	case (sep == "|" || sep == "|&") && shellInterpreters[inv.base] && readsScriptFromInput(inv.args):
		recordViolation(s.rep, "HISS-08", s.rel, line, "", "Text piped into "+inv.base+" is evaluated as code; run a reviewed script file instead")
	case runsSubstitution(inv):
		recordViolation(s.rep, "HISS-08", s.rel, line, "", "A process substitution run as a script evaluates generated text as code")
	case unboundedCurl(inv):
		recordViolation(s.rep, "HISS-02", s.rel, line, "", "curl without --max-time waits on the network without a deadline")
	}
}

// curlInfoOptions make curl print information and exit without a transfer.
var curlInfoOptions = map[string]bool{"--version": true, "-V": true, "--help": true, "-h": true, "--manual": true, "-M": true}

// unboundedCurl reports a curl transfer that nothing bounds: no timeout command around it, no
// --max-time, and not an invocation that only prints information.
func unboundedCurl(inv shellInvocation) bool {
	if inv.base != "curl" || inv.timeout || curlHasMaxTime(inv.args) {
		return false
	}
	for _, a := range inv.args {
		if curlInfoOptions[a] {
			return false
		}
	}
	return true
}

// readsScriptFromInput reports whether a shell given args reads its script from standard input:
// no script file operand and no -c command string.
func readsScriptFromInput(args []string) bool {
	for _, a := range args {
		switch {
		case a == "-" || a == "--" || a == "-s":
			return true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c"):
			return false
		case !strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "+"):
			return false
		}
	}
	return true
}

// runsSubstitution reports source or . given a process substitution, and a shell given one as
// its script.
func runsSubstitution(inv shellInvocation) bool {
	if len(inv.args) == 0 {
		return false
	}
	if inv.base == "source" || inv.name == "." {
		return inv.args[0] == "<("
	}
	if !shellInterpreters[inv.base] {
		return false
	}
	for _, a := range inv.args {
		if !strings.HasPrefix(a, "-") {
			return a == "<("
		}
	}
	return false
}

// curlHasMaxTime reports whether curl's arguments bound the whole transfer: --max-time, or the
// short option -m alone or in a cluster such as -fsSLm.
func curlHasMaxTime(args []string) bool {
	for _, a := range args {
		if a == "--max-time" || strings.HasPrefix(a, "--max-time=") {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "m") {
			return true
		}
	}
	return false
}

// observe records the strict-mode options set's arguments, or a #! line's, enable: -e, -u and
// -o NAME, alone or in a cluster such as -euo pipefail.
func (st *shellStrict) observe(args []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		for _, c := range a[1:] {
			switch c {
			case 'e':
				st.errexit = true
			case 'u':
				st.nounset = true
			case 'o':
				if i+1 < len(args) {
					i++
					st.option(args[i])
				}
			}
		}
	}
}

// option records one long option name.
func (st *shellStrict) option(name string) {
	switch name {
	case "errexit":
		st.errexit = true
	case "nounset":
		st.nounset = true
	case "pipefail":
		st.pipefail = true
	}
}
