package util

import "strings"

// makefileSpecialVariables are the variables whose name starts with a dot that the allow-list
// accepts: the include boundary the splice adds and the default goal, which remake nothing.
var makefileSpecialVariables = map[string]bool{
	".PRAETOR_INCLUDE_BOUNDARY": true, ".DEFAULT_GOAL": true,
}

// makefileSpecialTargets are the dot targets the allow-list accepts as rule targets: they set
// flags for other rules and name no file Make builds. Any other target that starts with a dot
// (.SUFFIXES, .DEFAULT, a suffix rule) can change how a file is found or built.
var makefileSpecialTargets = map[string]bool{
	".PHONY": true, ".SILENT": true, ".ONESHELL": true, ".DELETE_ON_ERROR": true, ".PRECIOUS": true,
}

// makefileTargetForbidden are the bytes a rule target may not hold for the allow-list to read it
// as a literal name: a reference or pattern, a grouped-target separator, a wildcard, an archive
// member, an escape, a quote or a redirect.
const makefileTargetForbidden = "$%&()[]{}*?~\\\"'`<>|"

// makefileCLIVariableLines holds the lines of MakefileCLIVariable after line-ending normalisation
// and trailing-space trimming.
var makefileCLIVariableLines = func() []string {
	normalized := strings.ReplaceAll(MakefileCLIVariable, "\r\n", "\n")
	raw := strings.Split(normalized, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}()

// makefileIsCLIVariableLine reports whether line matches one of the lines of MakefileCLIVariable
// by exact comparison after line-ending normalisation and trailing-space trim.
func makefileIsCLIVariableLine(line string) bool {
	norm := strings.TrimRight(strings.ReplaceAll(line, "\r", ""), " \t")
	for _, expected := range makefileCLIVariableLines {
		if norm == expected {
			return true
		}
	}
	return false
}

// makefileIsMakefileName reports whether name is one of the names Make treats as its makefile.
// Make remakes its own makefile and re-execs, so a rule for any of these names can rewrite an include.
func makefileIsMakefileName(name string) bool {
	return name == "Makefile" || name == "makefile" || name == "GNUmakefile"
}

// makefileTrackedLine records one logical line together with the file and 1-indexed line number
// where it appeared, so a refusal note can name the exact location that caused it.
type makefileTrackedLine struct {
	text string
	file string
	line int
}

// makefileTrackedLogicalLines returns the logical lines of data with each line's file and 1-based
// start line recorded, and false when the file exceeds the line bound.
func makefileTrackedLogicalLines(data, file string) ([]makefileTrackedLine, bool) {
	physical := strings.Split(data, "\n")
	lines := make([]makefileTrackedLine, 0, min(len(physical), MaxMakefileLines))
	for start := 0; start < len(physical) && start < MaxMakefileLines; {
		line, next, whole := makefileJoin(physical, start)
		if !whole {
			return lines, false
		}
		lines = append(lines, makefileTrackedLine{
			text: strings.TrimSuffix(line, "\r"),
			file: file,
			line: start + 1,
		})
		start = next
	}
	return lines, len(physical) <= MaxMakefileLines
}

// makefileTrackedLiteralShapes reports whether every line in lines is an allowed literal shape.
// When a line is refused, it returns false, the file and line number of the first refused line,
// and a description of its shape.
func makefileTrackedLiteralShapes(lines []makefileTrackedLine, followed []string) (bool, string, int, string) {
	operands := make(map[string]struct{}, len(followed))
	for _, operand := range followed {
		operands[makefileNormalizeName(operand)] = struct{}{}
	}
	allowCLIVariable := !makefileAssignsShellCombined(lines)
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		tl := lines[index]
		kind := scanner.next(tl.text)
		if scanner.lost || kind == makefileDefineLine || kind == makefileUnsureLine {
			shape := makefileRefusedShape(tl.text, kind, scanner.lost, operands)
			return false, tl.file, tl.line, shape
		}
		if kind == makefileSyntaxLine && !makefileAllowedShape(strings.TrimSpace(tl.text), operands, allowCLIVariable) {
			shape := makefileRefusedShape(strings.TrimSpace(tl.text), kind, false, operands)
			return false, tl.file, tl.line, shape
		}
	}
	return true, "", 0, ""
}

// makefileAssignsShell reports whether line assigns one of the shell variables Make runs $(shell)
// through: SHELL, .SHELLFLAGS or MAKESHELL. Leading override, export, private and unexport
// keywords are stripped before the comparison.
func makefileAssignsShell(line string) bool {
	assign, colon, _ := makefileSplit(line)
	if assign < 0 || (colon >= 0 && colon < assign) {
		return false
	}
	for _, word := range makefileNameWords(line[:assign]) {
		switch word {
		case "SHELL", ".SHELLFLAGS", "MAKESHELL":
			return true
		}
	}
	return false
}

// makefileAssignsShellCombined reports whether any syntax line of lines assigns SHELL, .SHELLFLAGS
// or MAKESHELL.
func makefileAssignsShellCombined(lines []makefileTrackedLine) bool {
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		tl := lines[index]
		if scanner.next(tl.text) == makefileSyntaxLine && makefileAssignsShell(strings.TrimSpace(tl.text)) {
			return true
		}
	}
	return false
}

// makefileParsesText reports whether line holds a call that parses text, runs a command or writes
// a file while Make reads it: eval, guile, shell, file, or a call (whose first argument may name
// any of them).
func makefileParsesText(line string) bool {
	return makefileCallsFunction(line, func(name, _ string) bool {
		switch name {
		case "eval", "guile", "shell", "file", "call":
			return true
		}
		return false
	})
}

// makefileBlankOrComment reports whether line is empty or starts with a comment character.
func makefileBlankOrComment(line string) bool {
	return line == "" || strings.HasPrefix(line, "#")
}

// makefileAllowedParsedText reports whether line is allowed despite holding text-parsing syntax.
func makefileAllowedParsedText(line string, allowCLIVariable bool) bool {
	return allowCLIVariable && makefileIsCLIVariableLine(line)
}

// makefileAllowedShape reports whether one trimmed syntax line is an allowed shape.
func makefileAllowedShape(line string, operands map[string]struct{}, allowCLIVariable bool) bool {
	if makefileBlankOrComment(line) {
		return true
	}
	if makefileParsesText(line) && !makefileAllowedParsedText(line, allowCLIVariable) {
		return false
	}
	if makefileAllowedDirective(line) {
		return true
	}
	assign, colon, _ := makefileSplit(line)
	if assign >= 0 && (colon < 0 || assign < colon) {
		return makefileAllowedAssignment(line, assign)
	}
	if colon >= 0 {
		return makefileAllowedRule(line[:colon], line[colon:], operands)
	}
	return false
}

func makefileAllowedDirective(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	if makefileConditionalLine(line, fields) {
		return true
	}
	switch fields[0] {
	case "include", "-include", "sinclude":
		return true
	}
	return false
}

// makefileAllowedAssignment reports whether the assignment whose operator starts at line[assign]
// binds one literal name that is not VPATH or a dot name, with a value Make does not run: "!="
// runs its value as a command and expands the output as makefile text.
func makefileAllowedAssignment(line string, assign int) bool {
	if line[assign] == '!' {
		return false
	}
	words := makefileNameWords(line[:assign])
	if len(words) != 1 || strings.ContainsAny(words[0], "$\\") || words[0] == "VPATH" {
		return false
	}
	return !strings.HasPrefix(words[0], ".") || makefileSpecialVariables[words[0]]
}

// makefileAllowedRule reports whether head, the target part of a rule, holds literal targets only,
// none of which equals a followed operand after Make's normalisation or starts with a dot unless
// it is a flag-setting special target. rest starts at the rule's colon: a double colon rule can be
// built by a recipe Make picks on its own, so it is refused.
func makefileAllowedRule(head, rest string, operands map[string]struct{}) bool {
	fields := strings.Fields(head)
	if len(fields) == 0 || strings.HasPrefix(rest, "::") {
		return false
	}
	for _, field := range fields {
		if strings.ContainsAny(field, makefileTargetForbidden) {
			return false
		}
		name := makefileNormalizeName(field)
		if _, followed := operands[name]; followed || makefileIsMakefileName(name) {
			return false
		}
		if strings.HasPrefix(name, ".") && !makefileSpecialTargets[name] {
			return false
		}
	}
	return true
}

// makefileRefusedShape classifies the shape of a line that was refused by makefileTrackedLiteralShapes.
func makefileRefusedShape(line string, kind int, lost bool, operands map[string]struct{}) string {
	if s := makefileScannerStateShape(kind, lost); s != "" {
		return s
	}
	if makefileParsesText(line) {
		return makefileParsesTextShape(line)
	}
	if s := makefileDirectiveShape(strings.Fields(line)); s != "" {
		return s
	}
	return makefileGrammarShape(line, operands)
}

func makefileScannerStateShape(kind int, lost bool) string {
	if lost {
		return "unbalanced conditional"
	}
	if kind == makefileDefineLine {
		return "define block"
	}
	if kind == makefileUnsureLine {
		return "unrecognized directive"
	}
	return ""
}

func makefileGrammarShape(line string, operands map[string]struct{}) string {
	assign, colon, _ := makefileSplit(line)
	if assign >= 0 && (colon < 0 || assign < colon) {
		return makefileAssignmentShape(line, assign)
	}
	if colon >= 0 {
		return makefileRuleShape(line[:colon], line[colon:], operands)
	}
	if strings.Contains(line, "$") {
		return "bare expansion"
	}
	return "unsupported syntax"
}

func makefileDirectiveShape(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "vpath":
		return "vpath directive"
	case "load":
		return "load directive"
	case "export":
		return "bare export"
	case "unexport":
		return "unexport directive"
	}
	return ""
}

func makefileParsesTextShape(line string) string {
	var called string
	makefileCallsFunction(line, func(name, _ string) bool {
		switch name {
		case "eval", "guile", "shell", "file", "call":
			called = name
			return true
		}
		return false
	})
	if called != "" {
		return "call to $(" + called + ")"
	}
	return "parses makefile text"
}

func makefileAssignmentShape(line string, assign int) string {
	if line[assign] == '!' {
		return "command assignment"
	}
	words := makefileNameWords(line[:assign])
	if len(words) != 1 {
		return "bare export"
	}
	if strings.ContainsAny(words[0], "$\\") {
		return "computed variable name"
	}
	if words[0] == "VPATH" {
		return "VPATH assignment"
	}
	if strings.HasPrefix(words[0], ".") {
		return "special variable assignment"
	}
	return "assignment"
}

func makefileRuleShape(head, rest string, operands map[string]struct{}) string {
	if strings.HasPrefix(rest, "::") {
		return "double-colon rule"
	}
	fields := strings.Fields(head)
	for _, field := range fields {
		if shape := makefileRuleTargetShape(field, operands); shape != "" {
			return shape
		}
	}
	return "rule"
}

func makefileRuleTargetShape(field string, operands map[string]struct{}) string {
	if strings.Contains(field, "$") {
		return "computed target"
	}
	if strings.Contains(field, "%") {
		return "pattern rule"
	}
	if strings.Contains(field, "&") {
		return "grouped target"
	}
	if strings.ContainsAny(field, makefileTargetForbidden) {
		return "unsupported target syntax"
	}
	name := makefileNormalizeName(field)
	if _, followed := operands[name]; followed {
		return "rule remakes " + name
	}
	if makefileIsMakefileName(name) {
		return "rule remakes " + name
	}
	if strings.HasPrefix(name, ".") && !makefileSpecialTargets[name] {
		return makefileSpecialDotTargetShape(name)
	}
	return ""
}

func makefileSpecialDotTargetShape(name string) string {
	if name == ".SUFFIXES" {
		return ".SUFFIXES target"
	}
	if name == ".DEFAULT" {
		return ".DEFAULT target"
	}
	if strings.Count(name, ".") >= 2 {
		return "suffix rule"
	}
	return "special target " + name
}
