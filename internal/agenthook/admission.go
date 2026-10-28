// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"fmt"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/sentinel"
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

// admissionCheck holds the compiled shapes and scope/job settings of an enforcing policy
// (order engine-hook-build-scope-gate-20261027).
type admissionCheck struct {
	shapes    []*regexp.Regexp
	scopeForm bool
	divisor   int
	readHost  func() (sentinel.HostStats, error)
	numCPU    func() int
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
	divisor := settings.MaxJobsDivisor
	if divisor == 0 {
		divisor = config.DefaultAdmissionMaxJobsDivisor
	}
	if err := config.ValidateAdmissionMaxJobsDivisor(divisor); err != nil {
		return nil, err
	}
	sources := append(slices.Clone(builtinHeavyShapes), settings.Commands...)
	check := &admissionCheck{
		shapes:    make([]*regexp.Regexp, 0, len(sources)),
		scopeForm: settings.ScopeForm,
		divisor:   divisor,
		readHost:  admission.HostReader("."),
		numCPU:    runtime.NumCPU,
	}
	for index, source := range sources {
		compiled, err := regexp.Compile(source)
		if err != nil {
			return nil, fmt.Errorf("admission shape %d: %w", index, err)
		}
		check.shapes = append(check.shapes, compiled)
	}
	return check, nil
}

// CheckAdmission evaluates command against the admission settings alone
// (order engine-hook-build-scope-gate-20261027).
func CheckAdmission(settings config.AdmissionSettings, command string) (Verdict, error) {
	check, err := newAdmissionCheck(settings)
	if err != nil {
		return Verdict{Outcome: Deny}, err
	}
	if check == nil {
		return Verdict{Outcome: Allow}, nil
	}
	return check.judge(command), nil
}

// WithAdmissionHost overrides the host reader and CPU count of an enforcing policy in tests
// (order engine-hook-build-scope-gate-20261027).
func (p *Policy) WithAdmissionHost(readHost func() (sentinel.HostStats, error), cores int) *Policy {
	if p == nil || p.admission == nil {
		return p
	}
	if readHost != nil {
		p.admission.readHost = readHost
	}
	if cores > 0 {
		p.admission.numCPU = func() int { return cores }
	}
	return p
}

// judge refuses command when one of its simple commands is a heavy step outside `praetorctl
// admit` (or an admitted systemd-run scope), exceeds the job cap, or cannot be read.
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
	admitted, sawSystemdRun := false, false
	var scopeMissing []string
	for depth := 0; i < len(words) && depth < admissionMaxWrappers; depth++ {
		name := commandName(words[i])
		if privilegeInvocationPattern.MatchString(strings.Join(append([]string{name}, words[i+1:]...), " ")) {
			return refusal("HISS", evasionMessage+"; privilege escalation prohibited: "+name)
		}
		if step, isAdmit := admitStep(name, words, i); isAdmit {
			if step < 0 {
				return ""
			}
			admitted, i = true, step
			continue
		}
		if shape, ok := c.shape(name, words[i+1:]); ok {
			return c.judgeStep(name, shape, words[i+1:], admitted, sawSystemdRun, scopeMissing)
		}
		spec, wrapper := admissionWrappers[name]
		if !wrapper {
			return ""
		}
		next := commandAfter(words, i, spec)
		if name == "systemd-run" {
			sawSystemdRun = true
			scopeMissing = missingScopeArgs(words[i+1 : min(next, len(words))])
		}
		i = next
	}
	return ""
}

// admitStep reports whether the command at i is `praetorctl admit` and, when it is, the index of
// the step it admits: everything after admit's `--` (docs/guides/admission.md), so the step's job
// count is judged without a second copy of admit's flag list; -1 when admit names no step.
func admitStep(name string, words []string, i int) (int, bool) {
	if (name != "praetorctl" && name != "standardsctl") || i+1 >= len(words) || words[i+1] != "admit" {
		return 0, false
	}
	separator := slices.Index(words[i+2:], "--")
	if separator < 0 {
		return -1, true
	}
	return i + 2 + separator + 1, true
}

// judgeStep evaluates a matched heavy step against the job cap, `praetorctl admit`, and the
// systemd-run scope form (order engine-hook-build-scope-gate-20261027).
func (c *admissionCheck) judgeStep(name, shape string, args []string, admitted, sawSystemdRun bool, scopeMissing []string) string {
	if reason := c.jobCapRefusal(shape, args); reason != "" {
		return reason
	}
	if admitted {
		return ""
	}
	label := stepLabel(name, shape, args)
	if c.scopeForm && sawSystemdRun {
		return c.judgeScope(shape, label, scopeMissing)
	}
	return admissionRefusal(shape, label)
}

// judgeScope verifies a systemd-run scope wrapper and checks live host memory and swap readings
// through internal/admission (order engine-hook-build-scope-gate-20261027).
func (c *admissionCheck) judgeScope(shape, label string, missing []string) string {
	if len(missing) > 0 {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope incomplete for %s: missing %s; "+
			"run: systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no <command> or "+
			"praetorctl admit --label %s -- <command> (docs/guides/admission.md)",
			shape, strings.Join(missing, ", "), label))
	}
	stats, err := c.readHost()
	if err != nil {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope host reading failed for %s: %v (docs/guides/admission.md)", shape, err))
	}
	policy := admission.HostCappedPolicy(stats)
	policy.MaxLoad1 = 0
	verdict := admission.Judge(stats, policy)
	if !verdict.Admit {
		return refusal("HISS", fmt.Sprintf("heavy step systemd-run scope refused for %s: %s (readings: %s; docs/guides/admission.md)",
			shape, verdict.Reason, admission.ReadingsText(stats)))
	}
	return ""
}

// missingScopeArgs returns the required systemd-run scope arguments absent from args
// (order engine-hook-build-scope-gate-20261027).
func missingScopeArgs(args []string) []string {
	user, scope, memHigh, noExpand := false, false, false, false
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		switch args[i] {
		case "--user":
			user = true
		case "--scope":
			scope = true
		case "--expand-environment=no":
			noExpand = true
		default:
			if hasMemoryHighArg(args[i], args, i) {
				memHigh = true
			}
		}
	}
	var missing []string
	if !user {
		missing = append(missing, "--user")
	}
	if !scope {
		missing = append(missing, "--scope")
	}
	if !memHigh {
		missing = append(missing, "MemoryHigh")
	}
	if !noExpand {
		missing = append(missing, "--expand-environment=no")
	}
	return missing
}

func hasMemoryHighArg(arg string, args []string, index int) bool {
	if hasMemoryHighValue(strings.TrimPrefix(arg, "-p")) || hasMemoryHighValue(strings.TrimPrefix(arg, "--property=")) {
		return true
	}
	return (arg == "-p" || arg == "--property") && index+1 < len(args) && hasMemoryHighValue(args[index+1])
}

func hasMemoryHighValue(prop string) bool {
	val, ok := strings.CutPrefix(prop, "MemoryHigh=")
	return ok && val != ""
}

// jobCapRefusal refuses a heavy step whose -j/--jobs flag exceeds floor(cores/divisor) or is
// unbounded (order engine-hook-build-scope-gate-20261027).
func (c *admissionCheck) jobCapRefusal(shape string, args []string) string {
	raw, bare, found := findJobsArg(args)
	if !found {
		return ""
	}
	cores := max(c.numCPU(), 1)
	limit := max(cores/c.divisor, 1)
	if bare {
		return refusal("HISS", fmt.Sprintf("heavy step job count unbounded for %s: bare %s on %d cores (limit %d, cores/%d); "+
			"pass -j%d or lower (docs/guides/admission.md)", shape, raw, cores, limit, c.divisor, limit))
	}
	jobs, err := strconv.Atoi(raw)
	if err != nil || jobs < 1 || jobs > limit {
		return refusal("HISS", fmt.Sprintf("heavy step job count above cap for %s: %s jobs on %d cores (limit %d, cores/%d); "+
			"pass -j%d or lower (docs/guides/admission.md)", shape, raw, cores, limit, c.divisor, limit))
	}
	return ""
}

func findJobsArg(args []string) (value string, bare bool, found bool) {
	for i := 0; i < len(args) && i < admissionMaxWords; i++ {
		if args[i] == "--" {
			return "", false, false
		}
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		if value, bare, found := parseJobsToken(args[i], next); found {
			return value, bare, true
		}
	}
	return "", false, false
}

func parseJobsToken(arg, next string) (value string, bare bool, found bool) {
	if arg == "-j" || arg == "--jobs" {
		if allDigits(next) {
			return next, false, true
		}
		return arg, true, true
	}
	if rest, ok := strings.CutPrefix(arg, "--jobs="); ok {
		if rest == "" {
			return "--jobs", true, true
		}
		return rest, false, true
	}
	if strings.HasPrefix(arg, "-j") && allDigits(arg[2:]) {
		return arg[2:], false, true
	}
	return "", false, false
}

func allDigits(text string) bool {
	if text == "" || len(text) > 16 {
		return false
	}
	for i := 0; i < len(text) && i < 16; i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
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
