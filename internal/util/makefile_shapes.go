package util

import (
	"path"
	"slices"
	"strings"
)

// makefileSpecialVariables lists variables that have special meaning to GNU Make or control
// its execution, flags, or search paths. GNU Make manual section 6.14 ("Other Special Variables")
// lists MAKEFILE_LIST, .DEFAULT_GOAL, MAKE_RESTARTS, MAKE_TERMOUT, MAKE_TERMERR, .RECIPEPREFIX,
// .VARIABLES, .FEATURES, .INCLUDE_DIRS, and .EXTRA_PREREQS. Section 6.7 ("Target-specific Variable
// Values"), section 5.3.2 ("Choosing the Shell"), section 7.3 ("Testing Flags"), and section 3.7
// ("How Makefiles Are Remade") describe VPATH, SHELL, MAKESHELL, MAKEFILES, MAKEFLAGS, GNUMAKEFLAGS,
// MAKEOVERRIDES, MFLAGS, and SUFFIXES. Modifying any of these may cause Make to remake makefiles or
// execute commands during makefile parsing.
var makefileSpecialVariables = map[string]bool{
	// GNU Make manual section 6.14 ("Other Special Variables")
	"MAKEFILE_LIST":  true,
	".DEFAULT_GOAL":  true,
	"MAKE_RESTARTS":  true,
	"MAKE_TERMOUT":   true,
	"MAKE_TERMERR":   true,
	".RECIPEPREFIX":  true,
	".VARIABLES":     true,
	".FEATURES":      true,
	".INCLUDE_DIRS":  true,
	".EXTRA_PREREQS": true,

	// Additional variables cited in the operator decision
	"VPATH":         true,
	"SHELL":         true,
	"MAKESHELL":     true,
	"MAKEFILES":     true,
	"MAKEFLAGS":     true,
	"GNUMAKEFLAGS":  true,
	"MAKEOVERRIDES": true,
	"MFLAGS":        true,
	"SUFFIXES":      true,

	// Additional GNU Make execution variables
	"CURDIR":      true,
	"MAKELEVEL":   true,
	".SHELLFLAGS": true,
	".LOADED":     true,
}

// makefileSpecialTargets are the dot targets the allow-list accepts as rule targets: only
// .PHONY is allowed. Any other target that starts with a dot (.SUFFIXES, .DEFAULT, .SILENT,
// a suffix rule) can change how a file is found or built.
var makefileSpecialTargets = map[string]bool{
	".PHONY": true,
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

// makefileTrackedLiteralShapes reports whether every non-recipe line in lines is an allowed shape
// under the operator's single rule: GNU Make evaluates nothing while it reads the makefiles.
// When a line is refused, it returns false, the file and line number of the first refused line,
// and a description of its shape.
func makefileTrackedLiteralShapes(lines []makefileTrackedLine, followed []string) (bool, string, int, string) {
	operands := make(map[string]struct{}, len(followed))
	for _, operand := range followed {
		operands[makefileNormalizeName(operand)] = struct{}{}
	}
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		tl := lines[index]
		kind := scanner.next(tl.text)
		if scanner.lost || kind == makefileDefineLine || kind == makefileUnsureLine {
			shape := makefileRefusedShape(tl.text, kind, scanner.lost, operands)
			return false, tl.file, tl.line, shape
		}
		if kind == makefileRecipeLine {
			continue
		}
		trimmed := strings.TrimSpace(tl.text)
		if !makefileAllowedLine(trimmed, operands) {
			shape := makefileRefusedShape(trimmed, kind, false, operands)
			return false, tl.file, tl.line, shape
		}
	}
	return true, "", 0, ""
}

// makefileAllowedLine reports whether line qualifies under the single rule:
//  1. a comment;
//  2. a blank line;
//  3. include, -include or sinclude with literal operands;
//  4. a variable assignment (=, :=, ::=, ?=, +=) whose name matches [A-Za-z_][A-Za-z0-9_]*,
//     is not a GNU Make special variable and whose value contains no $;
//  5. an explicit rule whose targets and prerequisites contain no $, %, &: or :: and
//     whose targets are not a followed operand, Makefile, makefile, GNUmakefile,
//     or a dot special target other than .PHONY;
//  6. a conditional (ifeq/ifneq/ifdef/ifndef/else/endif) whose arguments contain no $.
//
// The only exception is the exact lines of util.MakefileCLIVariable.
func makefileAllowedLine(line string, operands map[string]struct{}) bool {
	if makefileBlankCommentOrCLI(line) || makefileAllowedInclude(line) {
		return true
	}
	fields := strings.Fields(line)
	if makefileConditionalLine(line, fields) {
		return !strings.Contains(line, "$")
	}
	return makefileAllowedGrammar(line, operands)
}

func makefileBlankCommentOrCLI(line string) bool {
	return line == "" || strings.HasPrefix(line, "#") || makefileIsCLIVariableLine(line)
}

func makefileAllowedInclude(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return false
	}
	switch fields[0] {
	case "include", "-include", "sinclude":
	default:
		return false
	}
	for _, operand := range fields[1:] {
		if !makefileLiteralPath(operand) {
			return false
		}
	}
	return true
}

func makefileAllowedGrammar(line string, operands map[string]struct{}) bool {
	assign, colon, _ := makefileSplit(line)
	switch {
	case assign >= 0 && (colon < 0 || assign < colon):
		return makefileAllowedAssignment(line, assign)
	case colon >= 0 && assign < 0:
		return makefileAllowedRule(line, colon, operands)
	default:
		return false
	}
}

func makefileAllowedAssignment(line string, assign int) bool {
	op := makefileAssignmentOperator(line, assign)
	if op == "" {
		return false
	}
	name := strings.TrimSpace(line[:assign])
	if !makefileMatchesIdentifier(name) || makefileSpecialVariables[name] {
		return false
	}
	value := line[assign+len(op):]
	return !strings.Contains(value, "$")
}

func makefileAssignmentOperator(line string, assign int) string {
	if assign < 0 || assign >= len(line) {
		return ""
	}
	tail := line[assign:]
	switch line[assign] {
	case '=':
		return "="
	case '+', '?':
		if strings.HasPrefix(tail, "+=") || strings.HasPrefix(tail, "?=") {
			return tail[:2]
		}
	case ':':
		return makefileColonAssignmentOperator(tail)
	}
	return ""
}

func makefileColonAssignmentOperator(tail string) string {
	if strings.HasPrefix(tail, "::=") {
		if strings.HasPrefix(tail, ":::=") {
			return ""
		}
		return "::="
	}
	if strings.HasPrefix(tail, ":=") {
		return ":="
	}
	return ""
}

func makefileMatchesIdentifier(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

func makefileAllowedRule(line string, colon int, operands map[string]struct{}) bool {
	head := line[:colon]
	rest := line[colon:]
	if strings.HasPrefix(rest, "::") || strings.Contains(head, "&") || strings.ContainsAny(head, "$%") {
		return false
	}
	if !makefileAllowedPrerequisites(line, colon, operands) {
		return false
	}
	return makefileAllowedTargets(head, operands)
}

func makefileAllowedPrerequisites(line string, colon int, operands map[string]struct{}) bool {
	endPrereq := len(line)
	for i := colon + 1; i < len(line); {
		kind, width := makefileOperatorAt(line, i)
		if kind == makefileEndToken {
			endPrereq = i
			break
		}
		i += width
	}
	prereqs := line[colon+1 : endPrereq]
	if strings.ContainsAny(prereqs, "$%=") || strings.Contains(prereqs, "&:") || strings.Contains(prereqs, "::") {
		return false
	}
	for _, field := range strings.Fields(prereqs) {
		name := makefileNormalizeName(field)
		if makefileImplicitRuleTargetOrPrereq(name, operands) {
			return false
		}
	}
	return true
}

func makefileAllowedTargets(head string, operands map[string]struct{}) bool {
	fields := strings.Fields(head)
	if len(fields) == 0 {
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
		if strings.HasPrefix(name, ".") && name != ".PHONY" {
			return false
		}
		if makefileImplicitRuleTargetOrPrereq(name, operands) {
			return false
		}
	}
	return true
}

func makefileImplicitRuleTargetOrPrereq(norm string, operands map[string]struct{}) bool {
	if makefileHasRCSSCCS(norm) {
		return true
	}
	for operand := range operands {
		if norm == operand {
			continue
		}
		base := path.Base(operand)
		if norm == base || MakefileImplicitSourceName(norm, base) {
			return true
		}
	}
	return false
}

// MakefileImplicitSourceName reports whether name is a file, path or directory a built-in rule
// may build base from: any normalised name containing base other than base itself, or that has an
// RCS or SCCS path element.
func MakefileImplicitSourceName(name, base string) bool {
	norm := makefileNormalizeName(name)
	if makefileHasRCSSCCS(norm) {
		return true
	}
	return base != "" && norm != base && strings.Contains(norm, base)
}

func makefileHasRCSSCCS(norm string) bool {
	for _, part := range strings.Split(norm, "/") {
		if part == "RCS" || part == "SCCS" {
			return true
		}
	}
	return false
}

var makefileTextParsingFunctions = []string{"eval", "guile", "shell", "file", "call"}

// makefileParsedFunction reports the name of the first text-parsing, command-running or
// file-writing function called on line (eval, guile, shell, file, or call).
func makefileParsedFunction(line string) string {
	var called string
	makefileCallsFunction(line, func(name, _ string) bool {
		if slices.Contains(makefileTextParsingFunctions, name) {
			called = name
			return true
		}
		return false
	})
	return called
}

// makefileParsesText reports whether line holds a call that parses text, runs a command or writes
// a file while Make reads it: eval, guile, shell, file, or a call (whose first argument may name
// any of them).
func makefileParsesText(line string) bool {
	return makefileParsedFunction(line) != ""
}

func makefileParsesTextShape(line string) string {
	if called := makefileParsedFunction(line); called != "" {
		return "call to $(" + called + ")"
	}
	return "parses makefile text"
}

// makefileRefusedShape classifies the shape of a line that was refused by makefileTrackedLiteralShapes.
func makefileRefusedShape(line string, kind int, lost bool, operands map[string]struct{}) string {
	if s := makefileScannerStateShape(kind, lost); s != "" {
		return s
	}
	if makefileParsesText(line) {
		return makefileParsesTextShape(line)
	}
	fields := strings.Fields(line)
	if makefileConditionalLine(line, fields) && strings.Contains(line, "$") {
		return "expansion in conditional"
	}
	if s := makefileDirectiveShape(fields); s != "" {
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

func makefileDirectiveShape(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "vpath":
		return "vpath directive"
	case "load", "-load":
		return "load directive"
	case "export":
		return "bare export"
	case "unexport":
		return "unexport directive"
	case "override":
		return "override directive"
	case "private":
		return "private directive"
	case "undefine":
		return "undefine directive"
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

func makefileAssignmentShape(line string, assign int) string {
	if line[assign] == '!' {
		return "command assignment"
	}
	name := strings.TrimSpace(line[:assign])
	if strings.ContainsAny(name, " \t") {
		return "bare export"
	}
	if strings.ContainsAny(name, "$\\") {
		return "computed variable name"
	}
	if name == "VPATH" {
		return "VPATH assignment"
	}
	if strings.HasPrefix(name, ".") || makefileSpecialVariables[name] {
		return "special variable assignment"
	}
	if strings.Contains(line[assign:], "$") {
		return "variable value expansion"
	}
	return "assignment"
}

func makefileRuleShape(head, rest string, operands map[string]struct{}) string {
	if strings.HasPrefix(rest, "::") {
		return "double-colon rule"
	}
	if strings.Contains(head, "&:") || strings.Contains(head, "&") {
		return "grouped target"
	}
	for _, field := range strings.Fields(head) {
		if shape := makefileRuleTargetShape(field, operands); shape != "" {
			return shape
		}
	}
	if shape := makefilePrerequisiteShape(rest, operands); shape != "" {
		return shape
	}
	return "rule"
}

func makefilePrerequisiteShape(rest string, operands map[string]struct{}) string {
	endPrereq := len(rest)
	for i := 1; i < len(rest); {
		kind, width := makefileOperatorAt(rest, i)
		if kind == makefileEndToken {
			endPrereq = i
			break
		}
		i += width
	}
	prereqs := rest[1:endPrereq]
	switch {
	case strings.Contains(prereqs, "$"):
		return "computed prerequisite"
	case strings.Contains(prereqs, "%"):
		return "pattern prerequisite"
	case strings.Contains(prereqs, "&:") || strings.Contains(prereqs, "&"):
		return "grouped target"
	case strings.Contains(prereqs, "::"):
		return "double-colon rule"
	}
	for _, field := range strings.Fields(prereqs) {
		name := makefileNormalizeName(field)
		if makefileImplicitRuleTargetOrPrereq(name, operands) {
			return "rule remakes " + name
		}
	}
	return ""
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
	if makefileImplicitRuleTargetOrPrereq(name, operands) {
		return "rule remakes " + name
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
