// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss/pyliteral"
)

type pythonDateScanner struct {
	src                                      sourceFile
	rep                                      *ScanReport
	text                                     string
	line, lineStart, depth                   int
	annotationLine                           int
	value                                    fixtureDateValue
	valueLine, valueStart, valueEnd, nesting int
}

func scanPythonFixtureDates(src sourceFile, rep *ScanReport) {
	if !isPythonTest(src.rel) {
		return
	}
	s := pythonDateScanner{src: src, rep: rep, text: string(src.data), line: 1}
	for index := 0; index < len(s.text); {
		index = s.step(index)
	}
	s.flush()
}

func (s *pythonDateScanner) step(index int) int {
	switch {
	case s.text[index] == '#':
		return s.comment(index)
	case pyliteral.At(s.text, index):
		return s.literal(index)
	case s.text[index] == '\n':
		s.line++
		s.lineStart = index + 1
	case isIdentByte(s.text[index]):
		for end := index + 1; end < len(s.text); end++ {
			if !isIdentByte(s.text[end]) {
				return end
			}
		}
		return len(s.text)
	case s.text[index] == '(':
		s.depth++
	case s.text[index] == ')':
		s.depth--
	}
	return index + 1
}

func (s *pythonDateScanner) comment(index int) int {
	end := len(s.text)
	if offset := strings.IndexByte(s.text[index:], '\n'); offset >= 0 {
		end = index + offset
	}
	// A trailing comment cannot exempt an earlier literal or the following line.
	if strings.TrimSpace(s.text[s.lineStart:index]) == "" && testDateAllowed(s.text[index:end]) {
		s.annotationLine = s.line + 1
	}
	return end
}

func (s *pythonDateScanner) literal(index int) int {
	decoded, end, err := pyliteral.Parse(s.text, index)
	if err != nil {
		recordViolation(s.rep, "HISS-15", s.src.rel, s.line, "", "test fixture string cannot be decoded: "+err.Error())
		return index + 1
	}
	allowed := s.annotationLine == s.line
	s.annotationLine = 0 // One annotation applies to one literal, even on a shared line.
	value := newFixtureDateValue(decoded, allowed)
	if s.valueEnd == 0 || !pythonDateJoin(s.text[s.valueEnd:index], s.nesting) {
		s.flush()
		s.value = value
		s.valueLine = s.line
		s.valueStart = index
	} else {
		s.value = s.value.add(value)
	}
	s.valueEnd, s.nesting = end, s.depth
	s.line += strings.Count(s.text[index:end], "\n")
	if offset := strings.LastIndexByte(s.text[index:end], '\n'); offset >= 0 {
		s.lineStart = index + offset + 1
	}
	return end
}

// Join only static adjacent literals or additions, never arguments or identifiers.
func pythonDateJoin(gap string, nesting int) bool {
	if strings.Contains(gap, "\n") && nesting == 0 && !strings.Contains(gap, "\\\n") {
		return false
	}
	stripper := literalStripper{syn: pythonSyntax}
	for _, line := range strings.Split(gap, "\n") {
		if strings.Trim(stripper.strip(line), " \t\r+()\\") != "" {
			return false
		}
	}
	return true
}

func (s *pythonDateScanner) flush() {
	if s.valueEnd > 0 && !s.value.allowed {
		checkFixtureDateText(s.value, s.text[s.valueStart:s.valueEnd], s.src.rel, s.valueLine, s.rep)
	}
	s.valueEnd = 0
}
