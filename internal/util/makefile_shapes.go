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

// makefileLiteralShapes reports whether every line of data, the Makefile with the followed
// fragments spliced in, is a literal shape the reader fully understands, and no rule target
// equals a followed operand. The shapes are a comment or blank line, a recipe line, a conditional
// whose branches hold allowed shapes, an include line, a variable assignment (makefileAllowedAssignment)
// and an explicit rule with literal targets (makefileAllowedRule). A define, an export of a name
// without a value, a vpath directive, a bare expansion, a load, a call that parses text, and any
// line the scanner cannot place make the text ambiguous, as on a Makefile with an unread include.
func makefileLiteralShapes(data string, followed []string) bool {
	lines, whole := makefileLogicalLines(data)
	if !whole {
		return false
	}
	operands := make(map[string]struct{}, len(followed))
	for _, operand := range followed {
		operands[makefileNormalizeName(operand)] = struct{}{}
	}
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		kind := scanner.next(lines[index])
		if scanner.lost || kind == makefileDefineLine || kind == makefileUnsureLine {
			return false
		}
		if kind == makefileSyntaxLine && !makefileAllowedShape(strings.TrimSpace(lines[index]), operands) {
			return false
		}
	}
	return true
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

// makefileAllowedShape reports whether one trimmed syntax line is an allowed shape.
func makefileAllowedShape(line string, operands map[string]struct{}) bool {
	if line == "" || strings.HasPrefix(line, "#") {
		return true
	}
	if makefileParsesText(line) {
		return false
	}
	fields := strings.Fields(line)
	if makefileConditionalLine(line, fields) {
		return true
	}
	assign, colon, _ := makefileSplit(line)
	switch {
	case assign >= 0 && (colon < 0 || assign < colon):
		return makefileAllowedAssignment(line, assign)
	case colon >= 0:
		return makefileAllowedRule(line[:colon], line[colon:], operands)
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
		if _, followed := operands[name]; followed {
			return false
		}
		if strings.HasPrefix(name, ".") && !makefileSpecialTargets[name] {
			return false
		}
	}
	return true
}
