// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
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
// only the next Go literal / Python physical line or spanning literal. Reference
// layouts use time.DateOnly, time.RFC3339 or the package layout constant.
func testDateAllowed(comment string) bool {
	comment = strings.TrimSpace(comment)
	comment = strings.TrimPrefix(comment, "//")
	comment = strings.TrimPrefix(comment, "#")
	comment = strings.TrimPrefix(comment, "/*")
	comment = strings.TrimSuffix(comment, "*/")
	return fixtureDateAllowance.MatchString(strings.TrimSpace(comment))
}

func scanGoFixtureDates(src sourceFile, rep *ScanReport) {
	if !strings.HasSuffix(src.rel, "_test.go") {
		return
	}
	fset := token.NewFileSet()
	file := fset.AddFile(src.rel, -1, len(src.data))
	var lexer scanner.Scanner
	lexer.Init(file, src.data, nil, scanner.ScanComments)
	previous := ""
	for i := 0; i <= len(src.data); i++ {
		pos, kind, text := lexer.Scan()
		if kind == token.EOF {
			return
		}
		if kind == token.STRING && !testDateAllowed(previous) {
			checkGoFixtureDate(text, src.rel, fset.Position(pos).Line, rep)
		}
		// gofmt may place a literal annotation before the preceding argument comma.
		if kind == token.COMMA && testDateAllowed(previous) {
			continue
		}
		previous = ""
		if kind == token.COMMENT {
			previous = text
		}
	}
}

func checkGoFixtureDate(text, rel string, line int, rep *ScanReport) {
	decoded, err := strconv.Unquote(text)
	if err != nil {
		return
	} // The Go parser separately records unparsed input.
	if text[0] == '`' {
		checkFixtureDateText(decoded, rel, line, rep)
		return
	}
	if fixtureDate.MatchString(decoded) {
		recordViolation(rep, "HISS-15", rel, line, "", "literal calendar date in test fixture; derive it from the test clock")
	}
}

func checkFixtureDateText(text, rel string, line int, rep *ScanReport) {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if fixtureDate.MatchString(lines[i]) {
			recordViolation(rep, "HISS-15", rel, line+i, "", "literal calendar date in test fixture; derive it from the test clock")
		}
	}
}

func isPythonTest(rel string) bool {
	name := filepath.Base(rel)
	return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") ||
		strings.HasSuffix(name, "_selftest.py") || name == "selftest.py"
}

func scanPythonFixtureDates(src sourceFile, rep *ScanReport) {
	if !isPythonTest(src.rel) {
		return
	}
	var literals []string
	comment, previous := "", ""
	allowed := false
	stripper := literalStripper{syn: pythonSyntax}
	stripper.literal = func(text string, _ int) { literals = append(literals, text) }
	stripper.comment = func(text string) { comment = text }
	lines := src.lines()
	for i := 0; i < len(lines); i++ {
		carried := stripper.fence != ""
		comment, literals = "", nil
		stripper.strip(lines[i])
		if !carried {
			allowed = testDateAllowed(previous) || testDateAllowed(comment)
		}
		if !allowed {
			for j := 0; j < len(literals); j++ {
				checkFixtureDateText(literals[j], src.rel, i+1, rep)
			}
		}
		previous = ""
		if strings.TrimSpace(lines[i]) == comment {
			previous = comment
		}
	}
}
