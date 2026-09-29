// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"regexp"
	"slices"
	"strings"
)

// Function headers of JavaScript and TypeScript, matched against a trimmed line with literals
// and comments stripped. Each pattern ends just past the opening parenthesis of the parameter
// list, or just past the arrow of a single-parameter arrow function, so the caller can find
// where the body opens.
var (
	// scriptFunctionHeader is a function declaration or a named function expression at the start
	// of a statement: [export [default]] [declare] [async] function [*] [name] [<T>] (.
	scriptFunctionHeader = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:declare\s+)?(?:async\s+)?function\b\s*\*?\s*([A-Za-z_$][\w$]*)?\s*(?:<[^()]*>)?\s*\(`)
	// scriptBindingHeader is a function bound to a const, let or var: a function expression
	// (group 2, with its own name in group 3), a parenthesized arrow, or a single-parameter
	// arrow (the parameter in group 4). Group 1 is the binding.
	scriptBindingHeader = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]*)?=\s*(?:async\s+)?(?:(function)\b\s*\*?\s*([A-Za-z_$][\w$]*)?\s*(?:<[^()]*>)?\s*\(|(?:<[^()]*>\s*)?\(|([A-Za-z_$][\w$]*)\s*=>)`)
	// scriptFieldHeader is a class field holding a function: [modifiers] name [?!] [: T] = ...,
	// with the same three right-hand forms as scriptBindingHeader.
	scriptFieldHeader = regexp.MustCompile(`^(?:(?:public|private|protected|static|readonly|override|declare|accessor)\s+)*(#?[A-Za-z_$][\w$]*)\s*[?!]?\s*(?::[^=]*)?=\s*(?:async\s+)?(?:(function)\b\s*\*?\s*(?:[A-Za-z_$][\w$]*)?\s*(?:<[^()]*>)?\s*\(|(?:<[^()]*>\s*)?\(|([A-Za-z_$][\w$]*)\s*=>)`)
	// scriptPropertyHeader is an object property holding a function: name: function (...) or
	// name: (...) => or name: x =>.
	scriptPropertyHeader = regexp.MustCompile(`^(#?[A-Za-z_$][\w$]*)\s*:\s*(?:async\s+)?(?:(function)\b\s*\*?\s*(?:[A-Za-z_$][\w$]*)?\s*\(|\(|([A-Za-z_$][\w$]*)\s*=>)`)
	// scriptMemberHeader is a method of a class or an object literal: [modifiers] [*] name
	// [?!] [<T>] (. As a statement, `name(args) {` is not JavaScript, so a line of this shape
	// whose parameter list is followed by a brace is a method.
	scriptMemberHeader = regexp.MustCompile(`^(?:(?:public|private|protected|static|async|get|set|readonly|override|abstract|declare|accessor)\s+)*\*?\s*(#?[A-Za-z_$][\w$]*)\s*[?!]?\s*(?:<[^()]*>)?\s*\(`)
)

// scriptKeywords are words that begin a statement or an expression and so never name a method,
// field or property a header pattern could mistake them for.
var scriptKeywords = []string{"if", "for", "while", "switch", "catch", "with", "return", "typeof",
	"function", "new", "await", "delete", "void", "yield", "throw", "do", "else", "try", "finally",
	"import", "export", "case", "in", "of", "instanceof", "let", "const", "var", "class", "super"}

// scriptCallee says how a function is reached from its own body (HISS-01).
type scriptCallee int

const (
	// scriptBareCallee is a declared or bound function, reached by its bare name.
	scriptBareCallee scriptCallee = iota
	// scriptThisCallee is a method or class field, reached through this.
	scriptThisCallee
	// scriptUndecided is an object property, whose this depends on the caller.
	scriptUndecided
)

// scriptHeader is a function header found on a line, before its body is known to open.
type scriptHeader struct {
	name string
	// alias is the second name that reaches a named function expression bound to a variable.
	alias  string
	callee scriptCallee
	// arrow is set when the body follows =>, which the parameter list or single parameter
	// precedes.
	arrow bool
	// at is the index in the line just past the match: past the opening parenthesis, or past
	// the arrow of a single-parameter arrow (paren false).
	at    int
	paren bool
}

// matchScriptHeader finds the function header a trimmed, stripped line opens, if any.
func matchScriptHeader(trimmed string) (scriptHeader, bool) {
	if m := scriptFunctionHeader.FindStringSubmatchIndex(trimmed); m != nil {
		name := submatch(trimmed, m, 1)
		if name == "" && strings.HasPrefix(trimmed, "export") {
			name = "default"
		}
		return scriptHeader{name: name, at: m[1], paren: true}, name != ""
	}
	if m := scriptBindingHeader.FindStringSubmatchIndex(trimmed); m != nil {
		return rightHandHeader(trimmed, m, submatch(trimmed, m, 1), submatch(trimmed, m, 3), scriptBareCallee, 2, 4), true
	}
	return matchScriptMember(trimmed)
}

// matchScriptMember finds a class field, object property or method header.
func matchScriptMember(trimmed string) (scriptHeader, bool) {
	patterns := []struct {
		re     *regexp.Regexp
		callee scriptCallee
	}{{scriptFieldHeader, scriptThisCallee}, {scriptPropertyHeader, scriptUndecided}}
	for _, p := range patterns {
		if m := p.re.FindStringSubmatchIndex(trimmed); m != nil {
			name := submatch(trimmed, m, 1)
			if slices.Contains(scriptKeywords, name) {
				return scriptHeader{}, false
			}
			return rightHandHeader(trimmed, m, name, "", p.callee, 2, 3), true
		}
	}
	m := scriptMemberHeader.FindStringSubmatchIndex(trimmed)
	if m == nil || slices.Contains(scriptKeywords, submatch(trimmed, m, 1)) {
		return scriptHeader{}, false
	}
	return scriptHeader{name: submatch(trimmed, m, 1), callee: scriptThisCallee, at: m[1], paren: true}, true
}

// rightHandHeader builds the header of a function on the right of an assignment or a property:
// a function expression when group fnGroup matched, a single-parameter arrow when paramGroup
// did, and a parenthesized arrow otherwise.
func rightHandHeader(line string, m []int, name, inner string, callee scriptCallee, fnGroup, paramGroup int) scriptHeader {
	h := scriptHeader{name: name, callee: callee, at: m[1], paren: true}
	switch {
	case m[2*fnGroup] >= 0:
		if inner != "" {
			h.name, h.alias = inner, name
		}
	case m[2*paramGroup] >= 0:
		h.arrow, h.paren = true, false
	default:
		h.arrow = true
	}
	return h
}

// submatch returns group g of a FindStringSubmatchIndex result, or "" when it did not match.
func submatch(s string, m []int, g int) string {
	if m[2*g] < 0 {
		return ""
	}
	return s[m[2*g]:m[2*g+1]]
}

// closeParen scans code from `from` with depth parentheses already open and returns the index
// of the parenthesis that closes them, or -1 with the depth still open at the end of the line.
// A brace met first means the parameter list holds an object pattern or type this scanner does
// not follow, and ends the search with depth -1.
func closeParen(code string, from, depth int) (int, int) {
	for i := from; i < len(code); i++ {
		switch code[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, 0
			}
		case '{', '}':
			return -1, -1
		}
	}
	return -1, depth
}

// bodyBrace returns the index of the brace that opens the function body in code after the
// parameter list closed at from-1, or -1 when no body opens on the line: an expression-bodied
// arrow, a declaration without a body, or a call rather than a header.
func bodyBrace(code string, from int, arrow bool) int {
	i := skipBlanks(code, from)
	if arrow {
		return arrowBodyBrace(code, i)
	}
	if i < len(code) && code[i] == '{' {
		return i
	}
	if i < len(code) && code[i] == ':' {
		return returnTypeBody(code, i+1)
	}
	return -1
}

// arrowBodyBrace returns the index of the brace that opens an arrow function's block body: the
// arrow follows the parameter list at i, after a return type annotation at most, and the brace
// follows the arrow. An expression body opens no block.
func arrowBodyBrace(code string, i int) int {
	at := strings.Index(code[i:], "=>")
	if at < 0 || (at > 0 && code[i] != ':') {
		return -1
	}
	i = skipBlanks(code, i+at+2)
	if i < len(code) && code[i] == '{' {
		return i
	}
	return -1
}

// returnTypeBody finds the body brace after a return type annotation. A brace that follows a
// type operator (`: {`, `| {`, `<{`, `, {`) opens an object type and is skipped with its
// contents; the first other brace opens the body. An arrow or a semicolon first means the
// annotation belongs to a declaration without a body.
func returnTypeBody(code string, from int) int {
	for i := from; i < len(code); i++ {
		switch code[i] {
		case ';':
			return -1
		case '=':
			if i+1 < len(code) && code[i+1] == '>' {
				return -1
			}
		case '{':
			if !opensObjectType(code[:i]) {
				return i
			}
			i = skipBraces(code, i)
		}
	}
	return -1
}

// opensObjectType reports whether a brace after before opens an object type.
func opensObjectType(before string) bool {
	prev := strings.TrimRight(before, " \t")
	return prev == "" || strings.IndexByte(":|&<,(", prev[len(prev)-1]) >= 0
}

// skipBraces returns the index of the brace closing the one at open, or the last index.
func skipBraces(code string, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(code) - 1
}

func skipBlanks(code string, i int) int {
	for i < len(code) && (code[i] == ' ' || code[i] == '\t') {
		i++
	}
	return i
}

// Bindings that shadow a function's name inside its body (HISS-01).
var (
	scriptDeclarator = regexp.MustCompile(`(?:^|[^\w$.])(?:const|let|var)\s+[^=;]*$`)
	scriptItemName   = regexp.MustCompile(`(?:^|[^\w$.])(?:function\s*\*?|class)\s*$`)
	scriptCatchParam = regexp.MustCompile(`(?:^|[^\w$.])catch\s*\(\s*$`)
	// scriptFunctionParam is a name inside the parameter list of a function expression written
	// on the line, such as the callback of xs.map(function (walk) { ... }).
	scriptFunctionParam = regexp.MustCompile(`(?:^|[^\w$.])function\b\s*\*?\s*[\w$]*\s*\([^()]*$`)
)

// scriptBinds reports whether code binds name as a local: a const, let or var declarator
// (destructuring included), a nested function or class of that name, a catch parameter, or a
// parameter of an arrow function written on the line. A bare call then reaches the local.
func scriptBinds(code, name string) bool {
	at := nextIdent(code, name, 0)
	for i := 0; i < len(code) && at >= 0; i++ {
		if scriptDeclaresAt(code, at, name) {
			return true
		}
		at = nextIdent(code, name, at+1)
	}
	return false
}

// scriptDeclaresAt reports whether the occurrence of name at at is a binding.
func scriptDeclaresAt(code string, at int, name string) bool {
	if at > 0 && (code[at-1] == '$' || code[at-1] == '#') {
		return false
	}
	before := code[:at]
	if scriptDeclarator.MatchString(before) || scriptItemName.MatchString(before) ||
		scriptCatchParam.MatchString(before) || scriptFunctionParam.MatchString(before) {
		return true
	}
	return isArrowParam(code, at+len(name))
}

// isArrowParam reports whether the identifier ending at end is a parameter of an arrow
// function: followed directly by =>, or inside the parenthesized list the arrow follows.
func isArrowParam(code string, end int) bool {
	i := skipBlanks(code, end)
	if strings.HasPrefix(code[i:], "=>") {
		return true
	}
	if i < len(code) && code[i] == '(' {
		return false // a call, not a parameter
	}
	closing, _ := closeParen(code, i, 1)
	if closing < 0 {
		return false
	}
	rest := code[skipBlanks(code, closing+1):]
	return strings.HasPrefix(rest, "=>")
}
