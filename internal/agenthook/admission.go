// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// Host admission for heavy commands (#1131, docs/guides/admission.md). With
// hooks.admission.enforce set, the pre-tool policy refuses a command that starts a recognised
// heavy step outside `praetorctl admit`, and the refusal names the admitted form. The command
// is read the way the shell scanner reads a script (hiss.ShellCode): quoted text, comments and
// here-document bodies are not commands, so a commit message that mentions `make` is not a
// heavy step. A command the reader cannot follow is refused while enforcement is on.

const (
	// admissionMaxLines, admissionMaxSegments and admissionMaxWords bound the reading of one
	// command (HISS-02); the hook input is already bounded to 1 MiB.
	admissionMaxLines    = 4096
	admissionMaxSegments = 4096
	admissionMaxWords    = 256
	// admissionMaxWrappers bounds how many wrapper commands (nice, timeout, ...) are looked
	// through to the command they run.
	admissionMaxWrappers = 8
	// admissionShapeWords is how many words of a command its shapes see.
	admissionShapeWords = 16
)

// builtinHeavyShapes are the heavy steps the engine recognises: builds, test suites, gates and
// pushes, whose pre-push gate is a full verification. Each is RE2, matched against a command's
// words joined by one space, from the command's base name on. hooks.admission.commands adds
// shapes in the same form.
var builtinHeavyShapes = []string{
	`^(make|gmake|ninja)( |$)`,
	`^go (build|install|test)( |$)`,
	`^cargo (bench|build|check|clippy|install|test)( |$)`,
	`^meson (compile|test)( |$)`,
	`^cmake --build( |$)`,
	`^(npm|pnpm|yarn) (build|test|run build)( |$)`,
	`^(pytest|tox|nox)( |$)`,
	`^(praetorctl|standardsctl) gate( |$)`,
	`^git( -[Cc] [^ ]+)* push( |$)`,
	`^(docker|podman|buildah) (build|bud)( |$)`,
	`^(bazel|bazelisk) (build|test)( |$)`,
	`^(gradle|gradlew|mvn)( |$)`,
}

// wrapperSpec describes a command that runs another one: the options that take a separate
// value, and how many positional words precede the command it runs.
type wrapperSpec struct {
	values     []string
	positional int
}

// admissionWrappers are the commands looked through to the command they run, so `nice -n 10
// make` and `systemd-run --user --scope -p MemoryMax=8G -- go test ./...` are judged as make
// and go test.
var admissionWrappers = map[string]wrapperSpec{
	"chrt":    {positional: 1},
	"command": {},
	"builtin": {},
	"env":     {values: []string{"-u", "--unset", "-C", "--chdir"}},
	"exec":    {values: []string{"-a"}},
	"flock":   {values: []string{"-w", "--timeout", "-E", "--conflict-exit-code"}, positional: 1},
	"ionice":  {values: []string{"-c", "--class", "-n", "--classdata", "-p", "--pid", "-P", "--pgid", "-u", "--uid"}},
	"nice":    {values: []string{"-n", "--adjustment"}},
	"nohup":   {},
	"setsid":  {},
	"stdbuf":  {values: []string{"-i", "-o", "-e"}},
	"systemd-run": {values: []string{"-p", "--property", "-u", "--unit", "-E", "--setenv", "--slice", "-M", "--machine", "-H", "--host",
		"--uid", "--gid", "--nice", "--working-directory", "--description"}},
	"taskset": {positional: 1},
	"time":    {values: []string{"-f", "--format", "-o", "--output"}},
	"timeout": {values: []string{"-k", "--kill-after", "-s", "--signal"}, positional: 1},
	"xargs": {values: []string{"-a", "--arg-file", "-d", "--delimiter", "-E", "-e", "-I", "-i", "-L", "-l", "-n", "--max-args",
		"-P", "--max-procs", "-s", "--max-chars"}},
}

// admissionPrelude are the shell words that may stand before a command name in one segment.
var admissionPrelude = []string{"!", "if", "then", "else", "elif", "do", "while", "until"}

var (
	assignmentWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	labelWord      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,30}$`)
	// segmentSeparators end one simple command: lists, pipes, subshells, groups and command
	// substitutions (the lexer writes a backquoted one as $(...)).
	segmentSeparators = strings.NewReplacer(";", "\n", "&", "\n", "|", "\n", "(", "\n", ")", "\n", "{", "\n", "}", "\n")
)

// admissionCheck holds the compiled shapes of an enforcing policy.
type admissionCheck struct {
	shapes []*regexp.Regexp
}

// newAdmissionCheck compiles the built-in shapes and hooks.admission.commands; it returns nil
// when the settings do not enforce admission.
func newAdmissionCheck(settings config.AdmissionSettings) (*admissionCheck, error) {
	if !settings.Enforce {
		return nil, nil
	}
	if err := config.ValidateAdmissionCommands(settings.Commands); err != nil {
		return nil, err
	}
	sources := append(slices.Clone(builtinHeavyShapes), settings.Commands...)
	check := &admissionCheck{shapes: make([]*regexp.Regexp, 0, len(sources))}
	for index, source := range sources {
		compiled, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("admission shape %d: %w", index, err)
		}
		check.shapes = append(check.shapes, compiled)
	}
	return check, nil
}

// judge refuses command when one of its simple commands is a heavy step outside `praetorctl
// admit`, or when it cannot be read.
func (c *admissionCheck) judge(command string) Verdict {
	lines, ok := hiss.ShellCode(command, admissionMaxLines)
	if !ok {
		return Verdict{Outcome: Deny, Reason: refusal("HISS", "heavy-step check: command unreadable (open quote, open here-document "+
			"or over bound); split command or write it to file first")}
	}
	segments := strings.Split(segmentSeparators.Replace(strings.Join(lines, "\n")), "\n")
	if len(segments) > admissionMaxSegments {
		return Verdict{Outcome: Deny, Reason: refusal("HISS", fmt.Sprintf("heavy-step check: over %d simple commands; split command", admissionMaxSegments))}
	}
	for index := 0; index < len(segments) && index < admissionMaxSegments; index++ {
		if reason := c.segment(strings.Fields(segments[index])); reason != "" {
			return Verdict{Outcome: Deny, Reason: reason}
		}
	}
	return Verdict{Outcome: Allow}
}

// segment reports whether one simple command is a heavy step, looking through the wrappers
// before it, or a privilege escalator, and returns the refusal.
func (c *admissionCheck) segment(words []string) string {
	words = words[:min(len(words), admissionMaxWords)]
	i := skipPrelude(words)
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		// `praetorctl admit -- <step>` passes here: praetorctl is neither a shape nor a wrapper,
		// so the step after it is admit's argument, not a command of this segment.
		name := commandName(words[i])
		if privilegeInvocationPattern.MatchString(strings.Join(append([]string{name}, words[i+1:]...), " ")) {
			return refusal("HISS", evasionMessage+"; privilege escalation prohibited: "+name)
		}
		if shape, ok := c.shape(name, words[i+1:]); ok {
			return admissionRefusal(shape, stepLabel(name, shape, words[i+1:]))
		}
		spec, wrapper := admissionWrappers[name]
		if !wrapper {
			return ""
		}
		i = commandAfter(words, i, spec)
	}
	return ""
}

// shape returns the matched part of the first shape the command matches.
func (c *admissionCheck) shape(name string, args []string) (string, bool) {
	text := strings.Join(append([]string{name}, args[:min(len(args), admissionShapeWords)]...), " ")
	for index := 0; index < len(c.shapes) && index < len(builtinHeavyShapes)+config.MaxAdmissionCommands; index++ {
		if match := c.shapes[index].FindString(text); match != "" {
			return strings.TrimSpace(match), true
		}
	}
	return "", false
}

// skipPrelude returns the index of the first word that is neither a variable assignment nor a
// shell keyword that may stand before a command name.
func skipPrelude(words []string) int {
	i := 0
	for i < len(words) && i < admissionMaxWords && (assignmentWord.MatchString(words[i]) || slices.Contains(admissionPrelude, words[i])) {
		i++
	}
	return i
}

// advanceOptionWord checks for options or terminator and advances the index if a value is consumed.
func advanceOptionWord(word string, j int, spec wrapperSpec) (newJ int, done bool, isOpt bool) {
	if word == "--" {
		return j, true, true
	}
	if strings.HasPrefix(word, "-") && len(word) > 1 {
		if slices.Contains(spec.values, word) {
			j++
		}
		return j, false, true
	}
	return j, false, false
}

// commandAfter returns the index of the command a wrapper at index i runs: past its options,
// the values they take, assignments and its positional words.
func commandAfter(words []string, i int, spec wrapperSpec) int {
	positional, optionsDone := spec.positional, false
	j := i + 1
	for ; j < len(words) && j < admissionMaxWords; j++ {
		word := words[j]
		if !optionsDone {
			var isOpt bool
			j, optionsDone, isOpt = advanceOptionWord(word, j, spec)
			if isOpt {
				continue
			}
		}
		switch {
		case assignmentWord.MatchString(word):
		case positional > 0:
			positional--
		default:
			return j
		}
	}
	return j
}

// commandName is the base name of a command word, without a Windows .exe suffix.
func commandName(word string) string {
	return strings.TrimSuffix(path.Base(strings.ReplaceAll(word, `\`, "/")), ".exe")
}

// stepLabel names the step for `praetorctl admit --label`: the command and the last word of a
// shape that spans more than one word (go-test, git-push), or else the command and its first
// plain argument (make-verify-all).
func stepLabel(name, shape string, args []string) string {
	if words := strings.Fields(shape); len(words) > 1 && labelWord.MatchString(words[len(words)-1]) {
		return name + "-" + words[len(words)-1]
	}
	for index := 0; index < len(args) && index < admissionShapeWords; index++ {
		if labelWord.MatchString(args[index]) {
			return name + "-" + args[index]
		}
		if !strings.HasPrefix(args[index], "-") {
			break
		}
	}
	return name
}

// admissionRefusal names the heavy step and the admitted form of the command.
func admissionRefusal(shape, label string) string {
	return refusal("HISS", "heavy step needs host admission: "+shape+"; run: praetorctl admit --label "+label+
		" -- <command>, in background; admit waits for memory, swap and load headroom (docs/guides/admission.md)")
}
