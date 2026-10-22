// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "strings"

// QuoteStepper tracks open quotes and substitutions with bounded nesting (HISS-02).
type QuoteStepper struct {
	stack [maxShellNesting]shellFrame
	depth int
	limit bool
}

func (s *QuoteStepper) top() *shellFrame {
	if s.depth == 0 {
		return nil
	}
	return &s.stack[s.depth-1]
}

func (s *QuoteStepper) push(ctx shellContext) {
	if s.depth >= maxShellNesting {
		s.limit = true
		return
	}
	s.stack[s.depth] = shellFrame{ctx: ctx}
	s.depth++
}

func (s *QuoteStepper) pop() {
	if s.depth > 0 {
		s.depth--
	}
}

// ExceededLimit reports whether nesting depth exceeded maxShellNesting.
func (s *QuoteStepper) ExceededLimit() bool {
	return s.limit
}

// Open reports whether a quote or substitution context is currently open.
func (s *QuoteStepper) Open() bool {
	return s.depth > 0
}

// InQuote reports whether the current position is enclosed within quotes.
func (s *QuoteStepper) InQuote() bool {
	for d := 0; d < s.depth && d < maxShellNesting; d++ {
		switch s.stack[d].ctx {
		case shellDouble, shellSingle, shellBacktick:
			return true
		}
	}
	return false
}

// OpenQuote returns the quote character left open, or 0 if none is open.
func (s *QuoteStepper) OpenQuote() byte {
	for d := s.depth - 1; d >= 0 && d < maxShellNesting; d-- {
		switch s.stack[d].ctx {
		case shellSingle:
			return '\''
		case shellDouble:
			return '"'
		case shellBacktick:
			return '`'
		}
	}
	if s.depth > 0 {
		return '"'
	}
	return 0
}

// Step advances the quote state at text[i] and returns the index past consumed bytes.
func (s *QuoteStepper) Step(text string, i int) int {
	frame := s.top()
	if frame == nil {
		return s.stepTop(text, i)
	}
	switch frame.ctx {
	case shellSingle:
		return s.stepSingle(text, i)
	case shellDouble:
		return s.stepDouble(text, i)
	case shellCommand:
		return s.stepCommand(text, i, frame)
	case shellBacktick:
		return s.stepBacktick(text, i)
	case shellArithExpansion:
		return s.stepArith(text, i, frame)
	default:
		return i
	}
}

func (s *QuoteStepper) stepTop(text string, i int) int {
	switch text[i] {
	case '\\':
		if i+1 < len(text) {
			return i + 1
		}
	case '\'':
		s.push(shellSingle)
	case '"':
		s.push(shellDouble)
	}
	return i
}

func (s *QuoteStepper) stepSingle(text string, i int) int {
	if text[i] == '\'' {
		s.pop()
	}
	return i
}

func (s *QuoteStepper) stepDouble(text string, i int) int {
	switch text[i] {
	case '\\':
		if i+1 < len(text) {
			return i + 1
		}
	case '"':
		s.pop()
	case '`':
		s.push(shellBacktick)
	case '$':
		return s.stepDollar(text, i)
	}
	return i
}

func (s *QuoteStepper) stepDollar(text string, i int) int {
	rest := text[i+1:]
	if strings.HasPrefix(rest, "((") {
		s.push(shellArithExpansion)
		return i + 2
	}
	if strings.HasPrefix(rest, "(") {
		s.push(shellCommand)
		return i + 1
	}
	return i
}

func (s *QuoteStepper) stepCommand(text string, i int, frame *shellFrame) int {
	switch text[i] {
	case '\\':
		if i+1 < len(text) {
			return i + 1
		}
	case '\'':
		s.push(shellSingle)
	case '"':
		s.push(shellDouble)
	case '`':
		s.push(shellBacktick)
	case '$':
		return s.stepDollar(text, i)
	case '(':
		frame.depth++
	case ')':
		if frame.depth == 0 {
			s.pop()
		} else {
			frame.depth--
		}
	}
	return i
}

func (s *QuoteStepper) stepBacktick(text string, i int) int {
	switch text[i] {
	case '\\':
		if i+1 < len(text) {
			return i + 1
		}
	case '`':
		s.pop()
	case '\'':
		s.push(shellSingle)
	case '"':
		s.push(shellDouble)
	case '$':
		return s.stepDollar(text, i)
	}
	return i
}

func (s *QuoteStepper) stepArith(text string, i int, frame *shellFrame) int {
	switch text[i] {
	case '\\':
		if i+1 < len(text) {
			return i + 1
		}
	case '(':
		frame.depth++
	case ')':
		if frame.depth > 0 {
			frame.depth--
		} else if strings.HasPrefix(text[i+1:], ")") {
			s.pop()
			return i + 1
		}
	}
	return i
}
