// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"unicode"
)

const stopChars = ",;:&|!{}()[]\n\r \t"

// stripLeadingMinus removes one leading '-' from target, then trims blanks.
func stripLeadingMinus(target string) string {
	trimmed := strings.TrimSpace(target)
	trimmed = strings.TrimPrefix(trimmed, "-")
	trimmed = strings.TrimSuffix(trimmed, "!")
	return strings.TrimSpace(trimmed)
}

// boundRefusesOne reports whether an ordering comparison op with integer lit (0, 1, 2)
// refuses every value <= 1.
func boundRefusesOne(op string, lit int, targetLeft bool) bool {
	if lit < 0 || lit > 2 {
		return false
	}
	if targetLeft {
		return boundRefusesTargetLeft(op, lit)
	}
	return boundRefusesLitLeft(op, lit)
}

func boundRefusesTargetLeft(op string, lit int) bool {
	switch op {
	case ">", "<=":
		return lit >= 1
	case ">=", "<":
		return lit >= 2
	default:
		return false
	}
}

func boundRefusesLitLeft(op string, lit int) bool {
	switch op {
	case "<", ">=":
		return lit >= 1
	case "<=", ">":
		return lit >= 2
	default:
		return false
	}
}

func comparisonOpAt(code string, i int) string {
	if i >= len(code) {
		return ""
	}
	sub := code[i:]
	ops := []string{"<=", ">=", "==", "!=", "<", ">"}
	for _, op := range ops {
		if strings.HasPrefix(sub, op) {
			return op
		}
	}
	return ""
}

// extractAllGuards finds all guarded targets on code: comparison guards and call guards.
func extractAllGuards(code string) []string {
	var guards []string
	guards = appendComparisonGuards(code, guards)
	guards = appendCallGuards(code, guards)
	return guards
}

// appendComparisonGuards finds targets compared with integer literal 0, 1, or 2.
func appendComparisonGuards(code string, guards []string) []string {
	n := len(code)
	for i := 0; i < n; i++ {
		if opLen, op, lit, ok := matchOpLit(code, i); ok {
			if boundRefusesOne(op, lit, true) {
				guards = appendBackwardGuard(code, i, guards)
			}
			i += opLen
			continue
		}
		if opLen, op, lit, ok := matchLitOp(code, i); ok {
			after := i + 1 + opLen
			if boundRefusesOne(op, lit, false) {
				guards = appendForwardGuard(code, after, guards)
			}
			i = after
		}
	}
	return guards
}

func appendBackwardGuard(code string, i int, guards []string) []string {
	if target := scanTargetBackwards(code, i); target != "" {
		return appendUniqueGuard(guards, target)
	}
	return guards
}

func appendForwardGuard(code string, after int, guards []string) []string {
	if target := scanTargetForwards(code, after); target != "" {
		return appendUniqueGuard(guards, target)
	}
	return guards
}

func matchOpLit(code string, i int) (int, string, int, bool) {
	op := comparisonOpAt(code, i)
	if op == "" {
		return 0, "", 0, false
	}
	n := len(code)
	j := i + len(op)
	for j < n && (code[j] == ' ' || code[j] == '\t') {
		j++
	}
	if j < n && code[j] >= '0' && code[j] <= '2' {
		if j+1 == n || !isIdentByte(code[j+1]) {
			return (j + 1) - i, op, int(code[j] - '0'), true
		}
	}
	return 0, "", 0, false
}

func matchLitOp(code string, i int) (int, string, int, bool) {
	n := len(code)
	if i >= n || code[i] < '0' || code[i] > '2' {
		return 0, "", 0, false
	}
	if i > 0 && isIdentByte(code[i-1]) {
		return 0, "", 0, false
	}
	j := i + 1
	for j < n && (code[j] == ' ' || code[j] == '\t') {
		j++
	}
	op := comparisonOpAt(code, j)
	if op == "" {
		return 0, "", 0, false
	}
	return (j + len(op)) - (i + 1), op, int(code[i] - '0'), true
}

// updateDelimDepth updates depth for an open/close delimiter pair, returning handled and ok status.
func updateDelimDepth(ch, openCh, closeCh byte, depth *int) (bool, bool) {
	if ch == openCh {
		*depth++
		return true, true
	}
	if ch == closeCh {
		if *depth > 0 {
			*depth--
			return true, true
		}
		return true, false
	}
	return false, true
}

func stepParenDepth(ch byte, parens, brackets *int, forward bool) bool {
	openP, closeP := byte('('), byte(')')
	openB, closeB := byte('['), byte(']')
	if !forward {
		openP, closeP = ')', '('
		openB, closeB = ']', '['
	}
	if handled, ok := updateDelimDepth(ch, openP, closeP, parens); handled {
		return ok
	}
	if handled, ok := updateDelimDepth(ch, openB, closeB, brackets); handled {
		return ok
	}
	if *parens == 0 && *brackets == 0 && isStopChar(ch) {
		return false
	}
	return true
}

func scanTargetBackwards(code string, before int) string {
	j := before - 1
	for j >= 0 && (code[j] == ' ' || code[j] == '\t') {
		j--
	}
	if j < 0 {
		return ""
	}
	end := j + 1
	parens, brackets := 0, 0
	for j >= 0 {
		if !stepParenDepth(code[j], &parens, &brackets, false) {
			break
		}
		j--
	}
	target := strings.TrimSpace(code[j+1 : end])
	return cleanTarget(target)
}

func scanTargetForwards(code string, after int) string {
	n := len(code)
	j := after
	for j < n && (code[j] == ' ' || code[j] == '\t') {
		j++
	}
	start := j
	parens, brackets := 0, 0
	for j < n {
		if !stepParenDepth(code[j], &parens, &brackets, true) {
			break
		}
		j++
	}
	target := strings.TrimSpace(code[start:j])
	return cleanTarget(target)
}

func isStopChar(ch byte) bool {
	return strings.IndexByte(stopChars, ch) >= 0
}

func cleanTarget(target string) string {
	target = strings.TrimSpace(target)
	target = strings.TrimPrefix(target, "not ")
	target = strings.TrimPrefix(target, "!")
	target = strings.TrimSpace(target)
	keywords := []string{"if", "elif", "while", "and", "or", "return"}
	for _, kw := range keywords {
		if strings.HasPrefix(target, kw+" ") {
			target = strings.TrimSpace(target[len(kw)+1:])
		}
	}
	if target == "" || isNumeric(target) {
		return ""
	}
	return target
}

func isNumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		if !unicode.IsDigit(rune(s[i])) {
			return false
		}
	}
	return true
}

func appendUniqueGuard(guards []string, target string) []string {
	if target == "" || len(guards) >= 64 {
		return guards
	}
	for _, g := range guards {
		if g == target {
			return guards
		}
	}
	return append(guards, target)
}

func isGuardCallName(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "safe") || strings.Contains(lower, "guard")
}

func appendArgsAsGuards(code string, afterIdent int, guards []string) []string {
	openParen := strings.IndexByte(code[afterIdent:], '(') + afterIdent
	args, _ := parseCallArgs(code, openParen)
	for _, arg := range args {
		arg = cleanTarget(stripLeadingMinus(arg))
		if arg != "" {
			guards = appendUniqueGuard(guards, arg)
		}
	}
	return guards
}

func appendCallGuards(code string, guards []string) []string {
	n := len(code)
	for i := 0; i < n; i++ {
		if !isIdentByte(code[i]) {
			continue
		}
		start := i
		for i < n && isIdentByte(code[i]) {
			i++
		}
		name := code[start:i]
		if isGuardCallName(name) && opensArgs(code, i) {
			guards = appendArgsAsGuards(code, i, guards)
		}
	}
	return guards
}

func adjustNestingDepth(ch byte, depth int) int {
	switch ch {
	case '(', '[', '{':
		return depth + 1
	case ')', ']', '}':
		return depth - 1
	default:
		return depth
	}
}

func appendTrimmedArg(args []string, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return args
	}
	return append(args, s)
}

func parseCallArgs(code string, openParen int) ([]string, int) {
	n := len(code)
	if openParen >= n || code[openParen] != '(' {
		return nil, openParen
	}
	var args []string
	depth := 1
	argStart := openParen + 1
	for i := openParen + 1; i < n; i++ {
		depth = adjustNestingDepth(code[i], depth)
		if depth == 0 {
			args = appendTrimmedArg(args, code[argStart:i])
			return args, i
		}
		if depth == 1 && code[i] == ',' {
			args = appendTrimmedArg(args, code[argStart:i])
			argStart = i + 1
		}
	}
	return args, n
}

func parseNegativeSignalCall(code string, at int, callName string) (string, string, bool) {
	after := at + len(callName)
	if !opensArgs(code, after) {
		return "", "", false
	}
	openParen := strings.IndexByte(code[after:], '(') + after
	args, _ := parseCallArgs(code, openParen)
	if len(args) == 0 {
		return "", "", false
	}
	first := strings.TrimSpace(args[0])
	if !strings.HasPrefix(first, "-") {
		return "", "", false
	}
	sig := ""
	if len(args) >= 2 {
		sig = strings.TrimSpace(args[1])
	}
	return stripLeadingMinus(first), sig, true
}
