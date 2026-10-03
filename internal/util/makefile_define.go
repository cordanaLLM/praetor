package util

import "strings"

// A define body is variable text: Make turns it into rules only when something parses its
// expansion as makefile syntax. Measured against GNU Make 4.4.1, three things do: an $(eval ...)
// or ${eval ...} call, a top-level line that is a bare expansion such as "$(name)" or
// "$(call name)", and an include that may hold either. A define used only through $(call ...) in a
// recipe declares no target, so it leaves ownership to this reader instead of to Make. A bare
// expansion needs no define to declare a rule: "$(if $(X),docs-lint: ; @echo x)" does, so rule text
// in its arguments leaves ownership to Make as well.

// makefileSilentFunctions are the functions whose expansion is always empty: info and warning
// print their argument and error stops Make, so no call to them expands into a rule.
var makefileSilentFunctions = []string{"info", "warning", "error"}

// makefileDirectiveWords are the first words that make a line a directive rather than a rule, an
// assignment or a bare expansion. Include forms are listed for completeness; makefileLineIsAmbiguous
// reports them before this list is consulted.
var makefileDirectiveWords = map[string]bool{
	"ifeq": true, "ifneq": true, "ifdef": true, "ifndef": true, "else": true, "endif": true,
	"define": true, "endef": true, "undefine": true, "vpath": true,
	"include": true, "-include": true, "sinclude": true,
}

// What a top-level Makefile line turned out to be for the bare-expansion check.
const (
	makefileLinePlain     = ""
	makefileLineDecided   = "decided"
	makefileLineExpansion = "expansion"
)

// makefileDefineTracker follows define ... endef nesting line by line the way GNU Make 4.4.1 reads
// it. At top level a define may carry the override, export, unexport or private modifiers, alone or
// combined. Inside a body only a line that does not open with a tab and whose first word is
// exactly "define" or "endef" changes the depth: a tab-indented endef stays body text, and an
// "override define" nested in a body opens nothing, so the first endef closes the outer block.
type makefileDefineTracker struct {
	depth   int
	defines bool
}

// body reports whether line belongs to a define block -- its opening line, a body line or its
// closing endef -- and advances the nesting. A tab-indented line at top level is a recipe line,
// never an opening; treating one as ordinary text only makes the reader stricter.
func (d *makefileDefineTracker) body(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return d.depth > 0
	}
	fields := strings.Fields(line)
	if d.depth == 0 {
		if !makefileOpensDefine(fields) {
			return false
		}
		d.depth, d.defines = 1, true
		return true
	}
	if len(fields) > 0 && fields[0] == "define" {
		d.depth++
	}
	if len(fields) > 0 && fields[0] == "endef" {
		d.depth--
	}
	return true
}

// makefileOpensDefine reports whether a top-level line opens a define block: its first word after
// the modifiers is "define" and the next word does not start with an assignment operator. Measured
// against GNU Make 4.4.1, "define := x" and "override define ?= x" bind a variable named define
// and a rule on the next line stays a rule, while "define name" and "define name =" open a block.
func makefileOpensDefine(fields []string) bool {
	index := makefileDirectiveIndex(fields)
	if index >= len(fields) || fields[index] != "define" {
		return false
	}
	if index+1 == len(fields) {
		return true
	}
	kind, _ := makefileOperatorAt(fields[index+1], 0)
	return kind != makefileAssignToken
}

// makefileOwnershipScan walks a Makefile once and records what only Make can resolve.
type makefileOwnershipScan struct {
	define    makefileDefineTracker
	colonVars map[string]bool
	expands   bool
}

func makefileDefineName(line string) string {
	fields := strings.Fields(line)
	if !makefileOpensDefine(fields) {
		return ""
	}
	idx := makefileDirectiveIndex(fields)
	if idx+1 < len(fields) {
		return fields[idx+1]
	}
	return ""
}

func makefileAssignmentNameAndValue(line string) (string, string, bool) {
	if !makefileBindsVariable(line) {
		return "", "", false
	}
	assign, _, _ := makefileSplit(line)
	_, width := makefileOperatorAt(line, assign)
	rawName := strings.TrimSpace(line[:assign])
	fields := strings.Fields(rawName)
	idx := makefileDirectiveIndex(fields)
	if idx >= len(fields) {
		return "", "", false
	}
	name := fields[idx]
	val := line[assign+width:]
	if comment := strings.Index(val, "#"); comment >= 0 {
		val = val[:comment]
	}
	return name, val, true
}

func makefilePropagateColonVars(colonVars map[string]bool, varDeps map[string][]string) {
	const maxFixpointIterations = 16
	for iter := 0; iter < maxFixpointIterations; iter++ {
		changed := false
		for name, deps := range varDeps {
			if colonVars[name] {
				continue
			}
			for _, dep := range deps {
				if colonVars[dep] {
					colonVars[name] = true
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}
}

// makefileRecordColonSource records colon-holding sources and referenced variable dependencies.
func makefileRecordColonSource(name, content string, colonVars map[string]bool, varDeps map[string][]string) {
	if name == "" {
		return
	}
	if makefileReferenceHoldsColon(content) || makefileHasUnredirectedShell(content) {
		colonVars[name] = true
	}
	if deps := makefileExtractReferencedVars(content); len(deps) > 0 {
		varDeps[name] = append(varDeps[name], deps...)
	}
}

// makefileColonVariables scans logical lines for variables whose assigned value or define body
// holds a colon, which can produce rules when expanded (HISS-02).
func makefileColonVariables(lines []makefileLogicalLine) map[string]bool {
	colonVars := make(map[string]bool)
	varDeps := make(map[string][]string)
	var define makefileDefineTracker
	var currentDefine string
	for i := 0; i < len(lines) && i < MaxMakefileLines; i++ {
		line := lines[i].text
		if strings.HasPrefix(line, "\t") {
			continue
		}
		if define.depth == 0 {
			currentDefine = makefileDefineName(line)
		}
		if define.body(line) {
			if define.depth > 0 {
				makefileRecordColonSource(currentDefine, line, colonVars, varDeps)
			}
			continue
		}
		if name, val, ok := makefileAssignmentNameAndValue(line); ok {
			makefileRecordColonSource(name, val, colonVars, varDeps)
		}
	}
	makefilePropagateColonVars(colonVars, varDeps)
	return colonVars
}

// makefileCallTargetName extracts the template name from a $(call name,...) or ${call name,...} reference.
func makefileCallTargetName(ref string) string {
	if len(ref) < 8 {
		return ""
	}
	last := len(ref)
	if ref[last-1] == ')' || ref[last-1] == '}' {
		last--
	}
	inner := ref[2:last]
	trimmed := strings.TrimLeft(inner, " \t")
	if !strings.HasPrefix(trimmed, "call ") && !strings.HasPrefix(trimmed, "call\t") {
		return ""
	}
	after := strings.TrimLeft(trimmed[5:], " \t")
	end := strings.IndexAny(after, ", \t)}")
	if end < 0 {
		return after
	}
	return after[:end]
}

// makefileReferenceTargetName extracts the variable name from a simple $(var) or ${var} reference.
func makefileReferenceTargetName(ref string) string {
	if len(ref) < 3 {
		return ""
	}
	last := len(ref)
	if ref[last-1] == ')' || ref[last-1] == '}' {
		last--
	}
	inner := ref[2:last]
	trimmed := strings.TrimSpace(inner)
	if strings.ContainsAny(trimmed, " \t(),$:") {
		return ""
	}
	return trimmed
}

// makefileExtractReferencedVars extracts all variable and call target names referenced in text
// at any nesting depth outside silent function calls (HISS-02).
func makefileExtractReferencedVars(text string) []string {
	var vars []string
	for i := 0; i < len(text) && i < MaxMakefileLineBytes; {
		if strings.HasPrefix(text[i:], "$$") {
			i += 2
			continue
		}
		if width := makefileSilentCallWidth(text[i:]); width > 0 {
			i += width
			continue
		}
		if strings.HasPrefix(text[i:], "$(") || strings.HasPrefix(text[i:], "${") {
			width := makefileReferenceWidth(text[i:])
			ref := text[i : i+width]
			if callName := makefileCallTargetName(ref); callName != "" {
				vars = append(vars, callName)
			}
			if varName := makefileReferenceTargetName(ref); varName != "" {
				vars = append(vars, varName)
			}
			i += 2
			continue
		}
		i++
	}
	return vars
}

func makefileShellIsRedirected(shellContent string) bool {
	return strings.Contains(shellContent, ">/dev/null") || strings.Contains(shellContent, "> /dev/null")
}

func makefileIsUnredirectedShellRef(ref string) bool {
	if len(ref) < 8 {
		return false
	}
	inner := ref[2:]
	if !strings.HasPrefix(inner, "shell ") && !strings.HasPrefix(inner, "shell\t") {
		return false
	}
	return !makefileShellIsRedirected(ref)
}

// makefileHasUnredirectedShell reports whether text contains an unredirected $(shell ...) call (HISS-02).
func makefileHasUnredirectedShell(text string) bool {
	for i := 0; i < len(text) && i < MaxMakefileLineBytes; {
		if strings.HasPrefix(text[i:], "$$") {
			i += 2
			continue
		}
		if width := makefileSilentCallWidth(text[i:]); width > 0 {
			i += width
			continue
		}
		if strings.HasPrefix(text[i:], "$(") || strings.HasPrefix(text[i:], "${") {
			width := makefileReferenceWidth(text[i:])
			if makefileIsUnredirectedShellRef(text[i : i+width]) {
				return true
			}
			i += 2
			continue
		}
		i++
	}
	return false
}

func (s *makefileOwnershipScan) referenceMayDefine(ref string) bool {
	if makefileReferenceHoldsColon(ref) || makefileHasUnredirectedShell(ref) {
		return true
	}
	for _, dep := range makefileExtractReferencedVars(ref) {
		if s.colonVars[dep] {
			return true
		}
	}
	return false
}

// expansionMayDefine reports whether a bare expansion line may define rules (HISS-02): a colon
// outside silent calls, an unredirected shell invocation, a call or reference of a colon-capable
// variable, multiple references like $(A)$(B), or an expansion beside a multi-line define.
func (s *makefileOwnershipScan) expansionMayDefine(line string) (mayDefine, expands bool) {
	refCount := 0
	for i := 0; i < len(line) && i < MaxMakefileLineBytes; {
		kind, width := makefileOperatorAt(line, i)
		if kind == makefileEndToken {
			break
		}
		if kind == makefileReferenceToken && makefileSilentCallWidth(line[i:]) == 0 {
			expands = true
			refCount++
			ref := line[i : i+width]
			if s.referenceMayDefine(ref) {
				return true, true
			}
		}
		i += width
	}
	if refCount >= 2 || (s.define.defines && expands) {
		return true, expands
	}
	return false, expands
}

// read classifies one logical line and reports whether it alone leaves ownership to Make: a line
// makefileLineIsAmbiguous reports, a define body line that calls eval, or a bare expansion that
// may define rules.
func (s *makefileOwnershipScan) read(line string) bool {
	if s.define.body(line) {
		return makefileCallsEval(line)
	}
	if strings.HasPrefix(line, "\t") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if makefileLineIsAmbiguous(trimmed) {
		return true
	}
	switch makefileLineKind(trimmed) {
	case makefileLineExpansion:
		mayDefine, expands := s.expansionMayDefine(trimmed)
		s.expands = s.expands || expands
		return mayDefine
	}
	return false
}

// unresolved reports whether the whole file leaves ownership to Make once every line is read: a
// define that is never closed swallows the rest of the file, text appended after it included, and
// a define beside a bare expansion may become rules.
func (s *makefileOwnershipScan) unresolved() bool {
	return s.define.depth > 0 || (s.define.defines && s.expands)
}

// makefileLineKind reports whether a trimmed top-level line is a bare expansion: it holds a variable
// reference Make expands but no assignment operator, no rule colon and no directive, so Make parses
// the expansion as makefile syntax. A comment, directive, assignment or rule is decided; a line
// with none of these and no reference is plain.
func makefileLineKind(line string) string {
	if strings.HasPrefix(line, "#") || makefileExportDirective(line) ||
		makefileDirectiveWords[makefileDirective(strings.Fields(line))] {
		return makefileLineDecided
	}
	assign, colon, reference := makefileSplit(line)
	switch {
	case assign >= 0 || colon >= 0:
		return makefileLineDecided
	case reference >= 0:
		return makefileLineExpansion
	}
	return makefileLinePlain
}

// makefileReferenceHoldsColon reports whether reference text holds a colon outside the info,
// warning and error calls nested in it and outside an escaped "$$".
func makefileReferenceHoldsColon(reference string) bool {
	for i := 0; i < len(reference) && i < MaxMakefileLineBytes; i += makefileColonScanStep(reference[i:]) {
		if reference[i] == ':' {
			return true
		}
	}
	return false
}

// makefileColonScanStep returns how far the colon scan moves from the start of text: past an
// escaped "$$" or a whole info, warning or error call, else one byte.
func makefileColonScanStep(text string) int {
	if strings.HasPrefix(text, "$$") {
		return 2
	}
	if width := makefileSilentCallWidth(text); width > 0 {
		return width
	}
	return 1
}

// makefileSilentCallWidth returns the byte length of the info, warning or error call text opens
// with, and 0 when text opens with anything else. Make reads the name as a function only when a
// blank follows it, so "$(info)" references a variable named info.
func makefileSilentCallWidth(text string) int {
	if !strings.HasPrefix(text, "$(") && !strings.HasPrefix(text, "${") {
		return 0
	}
	for _, name := range makefileSilentFunctions {
		if strings.HasPrefix(text[2:], name+" ") || strings.HasPrefix(text[2:], name+"\t") {
			return makefileReferenceWidth(text)
		}
	}
	return 0
}

// makefileContinues reports whether line ends in an odd run of backslashes, which joins the next
// physical line to it. A carriage return left by a CRLF file does not break the run.
func makefileContinues(line string) bool {
	line = strings.TrimSuffix(line, "\r")
	run := 0
	for run < len(line) && run < MaxMakefileLineBytes && line[len(line)-1-run] == '\\' {
		run++
	}
	return run%2 == 1
}

// makefileCallsEval reports whether line holds an $(eval ...) or ${eval ...} call.
func makefileCallsEval(line string) bool {
	return strings.Contains(line, "$(eval") || strings.Contains(line, "${eval")
}
