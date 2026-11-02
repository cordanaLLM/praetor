// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var fixtureDate = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)
var fixtureDateAllowance = regexp.MustCompile(`^testdate: (fact|golden): \S.+`)

// HISS-15: a clock fixture must derive its dates from the clock it tests. Comments
// are prose, not fixture values. An adjacent testdate annotation may identify a
// recorded fact (parsed, never compared with a clock), or golden bytes (compared
// verbatim, never read by a clock). Each annotation requires its reason and covers
// only the next Go literal / first literal on the next Python line. Reference
// layouts use time.DateOnly, time.RFC3339 or the package layout constant.
func testDateAllowed(comment string) bool {
	comment = strings.TrimSpace(comment)
	comment = strings.TrimPrefix(comment, "//")
	comment = strings.TrimPrefix(comment, "#")
	comment = strings.TrimPrefix(comment, "/*")
	comment = strings.TrimSuffix(comment, "*/")
	return fixtureDateAllowance.MatchString(strings.TrimSpace(comment))
}

// scanGoFixtureDates detects hardcoded calendar dates in Go test files.
func scanGoFixtureDates(src sourceFile, rep *ScanReport) {
	if !strings.HasSuffix(src.rel, "_test.go") {
		return
	}
	fset := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fset, src.rel, src.data, parser.SkipObjectResolution)
	if parseErr != nil || parsed == nil {
		return // scanGoSource records parse errors independently.
	}
	allowed := goDateAllowances(src, fset)
	values, merged := goDateValues(parsed, allowed)
	for node := range ast.Preorder(parsed) {
		value, ok := values[node]
		if !ok || merged[node] || value.allowed {
			continue
		}
		line := fset.Position(node.Pos()).Line
		spelling := string(src.data[fset.Position(node.Pos()).Offset:fset.Position(node.End()).Offset])
		checkFixtureDateText(value, spelling, src.rel, line, rep)
	}
}

// Permission is consumed by the next token; commas never carry it to another argument.
func goDateAllowances(src sourceFile, fset *token.FileSet) map[token.Pos]bool {
	file := fset.File(token.Pos(1))
	var lexer scanner.Scanner
	lexer.Init(file, src.data, nil, scanner.ScanComments)
	allowed := make(map[token.Pos]bool)
	previous := ""
	for i := 0; i <= len(src.data); i++ {
		pos, kind, text := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind == token.STRING {
			allowed[pos] = testDateAllowed(previous)
		}
		previous = ""
		if kind == token.COMMENT {
			previous = text
		}
	}
	return allowed
}

type fixtureDateValue struct {
	text, permission string
	allowed          bool
}

// newFixtureDateValue constructs a tracked fixture date text value.
func newFixtureDateValue(text string, allowed bool) fixtureDateValue {
	permission := "0"
	if allowed {
		permission = "1"
	}
	return fixtureDateValue{text: text, permission: strings.Repeat(permission, len(text)), allowed: allowed}
}

// add combines two fixture date values and their permission masks.
func (value fixtureDateValue) add(other fixtureDateValue) fixtureDateValue {
	return fixtureDateValue{
		text: value.text + other.text, permission: value.permission + other.permission,
		allowed: value.allowed && other.allowed,
	}
}

type goDateEvaluator struct {
	values  map[ast.Node]fixtureDateValue
	merged  map[ast.Node]bool
	allowed map[token.Pos]bool
}

// Reverse preorder evaluates constant additions bottom-up without recursive descent.
func goDateValues(file *ast.File, allowed map[token.Pos]bool) (map[ast.Node]fixtureDateValue, map[ast.Node]bool) {
	var nodes []ast.Node
	for node := range ast.Preorder(file) {
		nodes = append(nodes, node)
	}
	e := goDateEvaluator{make(map[ast.Node]fixtureDateValue), make(map[ast.Node]bool), allowed}
	for i := len(nodes) - 1; i >= 0; i-- {
		e.observe(nodes[i])
	}
	return e.values, e.merged
}

// observe evaluates an AST node for literal date values.
func (e *goDateEvaluator) observe(node ast.Node) {
	switch n := node.(type) {
	case *ast.BasicLit:
		e.literal(n)
	case *ast.ParenExpr:
		if value, ok := e.values[n.X]; ok {
			e.values[node], e.merged[n.X] = value, true
		}
	case *ast.BinaryExpr:
		left, lok := e.values[n.X]
		right, rok := e.values[n.Y]
		if n.Op == token.ADD && lok && rok {
			e.values[node] = left.add(right)
			e.merged[n.X], e.merged[n.Y] = true, true
		}
	}
}

// literal extracts and records string literals in Go AST nodes.
func (e *goDateEvaluator) literal(literal *ast.BasicLit) {
	if literal.Kind != token.STRING {
		return
	}
	decoded, err := strconv.Unquote(literal.Value)
	if err != nil {
		return // ParseFile has already validated each literal.
	}
	e.values[literal] = newFixtureDateValue(decoded, e.allowed[literal.Pos()])
}

// Decoded escapes may add virtual newlines. Locate contiguous dates in the source
// spelling; a date assembled from fragments is anchored at its expression's start.
func checkFixtureDateText(value fixtureDateValue, spelling, rel string, line int, rep *ScanReport) {
	matches := fixtureDate.FindAllStringIndex(value.text, -1)
	reported := make(map[int]bool)
	start := 0
	for i := 0; i < len(matches); i++ {
		match := matches[i]
		physical := line
		if offset := strings.Index(spelling[start:], value.text[match[0]:match[1]]); offset >= 0 {
			offset += start
			physical += strings.Count(spelling[:offset], "\n")
			start = offset + match[1] - match[0]
		}
		if !strings.Contains(value.permission[match[0]:match[1]], "0") {
			continue
		}
		if !reported[physical] {
			recordFixtureDate(rel, physical, rep)
			reported[physical] = true
		}
	}
}

// recordFixtureDate records a HISS-15 finding for an unexcused fixture date.
func recordFixtureDate(rel string, line int, rep *ScanReport) {
	recordViolation(rep, "HISS-15", rel, line, "", "literal calendar date in test fixture; derive it from the test clock")
}

// isPythonTest reports whether a path corresponds to a Python test file.
func isPythonTest(rel string) bool {
	name := filepath.Base(rel)
	return isPythonTestPath(rel) ||
		strings.HasSuffix(name, "_selftest.py") || name == "selftest.py"
}
