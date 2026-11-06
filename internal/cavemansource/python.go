package cavemansource

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss/pyliteral"
)

func extractPython(item discoveredInput, text string) ([]Source, error) {
	text = normalizeNewlines(text)
	starts := textLineStarts(text)
	sources := []Source{}
	outputIndex := 0
	for index := 0; index < len(text); {
		next, source, err := scanPythonSource(item, text, starts, index)
		if err != nil {
			return nil, err
		}
		if source != nil {
			source.Selector = fmt.Sprintf("python-output:%03d", outputIndex)
			sources = append(sources, *source)
			outputIndex++
		}
		index = next
	}
	return sources, nil
}

// scanPythonSource scans Python source tokens for caveman text checks.
func scanPythonSource(item discoveredInput, text string, starts []int, index int) (int, *Source, error) {
	switch {
	case text[index] == '#':
		return skipToNextLine(text, index), nil, nil
	case text[index] == '\'' || text[index] == '"':
		_, end, err := pyliteral.Parse(text, index)
		if err != nil {
			return 0, nil, fmt.Errorf("caveman source %s line %d: %w", item.path, lineNumber(starts, index), err)
		}
		return end, nil, nil
	case !pythonIdentifierStart(text[index]):
		return index + 1, nil, nil
	}
	return scanPythonCall(item, text, starts, index)
}

// scanPythonCall reads the identifier at index and, when it names an output call, decodes
// the call's text into a Source.
func scanPythonCall(item discoveredInput, text string, starts []int, index int) (int, *Source, error) {
	end := scanPythonIdentifier(text, index)
	name, callNameEnd := pythonOutputCallName(text, index, end)
	if name == "" {
		return end, nil, nil
	}
	open := skipPythonSpace(text, callNameEnd)
	if open >= len(text) || text[open] != '(' {
		return end, nil, nil
	}
	value, callEnd, classification, err := parsePythonOutput(text, open, name)
	line := lineNumber(starts, index)
	if err != nil {
		return 0, nil, fmt.Errorf("caveman source %s line %d: %s output unverified: %w", item.path, line, name, err)
	}
	source := sourceFrom(item, value, name, line)
	if classification != "" {
		source = sourceNotApplicable(item, value, name, line, classification)
	}
	return callEnd, &source, nil
}

func pythonOutputCallName(text string, start, end int) (string, int) {
	name := text[start:end]
	if pythonAttributeCall(text, start) {
		return "", end
	}
	switch name {
	case "print", "agent_message":
		return name, end
	case "stream":
		if callEnd, ok := pythonAttributeChain(text, end, ".write"); ok {
			return "stream.write", callEnd
		}
		return "", end
	case "sys":
		return pythonSystemOutputCall(text, end)
	}
	return "", end
}

func pythonSystemOutputCall(text string, end int) (string, int) {
	for _, candidate := range []string{".stderr.write", ".stdout.write", ".stdout.buffer.write"} {
		if callEnd, ok := pythonAttributeChain(text, end, candidate); ok {
			return "sys" + candidate, callEnd
		}
	}
	return "", end
}

func pythonAttributeChain(text string, start int, chain string) (int, bool) {
	index := start
	for _, part := range strings.Split(chain, ".") {
		if part == "" {
			continue
		}
		index = skipPythonHorizontalSpace(text, index)
		if index >= len(text) || text[index] != '.' {
			return start, false
		}
		index = skipPythonHorizontalSpace(text, index+1)
		if !strings.HasPrefix(text[index:], part) {
			return start, false
		}
		index += len(part)
	}
	return index, true
}

func parsePythonOutput(text string, open int, name string) (string, int, string, error) {
	argument, tail, callEnd, err := pythonFirstArgument(text, open)
	if err != nil {
		return "", 0, "", err
	}
	if name != "print" && strings.TrimSpace(tail) != "" {
		return "", 0, "", errors.New("multiple arguments are unsupported")
	}
	if name == "print" && !validPythonPrintTail(tail) {
		return "", 0, "", errors.New("multiple positional or unsupported keyword arguments")
	}
	value, ok, err := parsePythonTemplate(argument)
	if classification := pythonNotApplicable(text, callEnd); classification != "" {
		return strings.TrimSpace(argument), callEnd, classification, nil
	}
	if ok {
		return value, callEnd, "", nil
	}
	if err != nil {
		return "", 0, "", err
	}
	return "", 0, "", errors.New("computed output requires an adjacent caveman:not-applicable classification")
}

// pythonFirstArgument extracts the first argument expression from a Python call.
func pythonFirstArgument(text string, open int) (string, string, int, error) {
	state := pythonArgumentScan{argumentEnd: -1}
	for index := open + 1; index < len(text); index++ {
		if pyliteral.At(text, index) {
			end, err := pyliteral.Skip(text, index)
			if err != nil {
				return "", "", 0, err
			}
			index = end - 1
			continue
		}
		if state.observe(text[index], index) {
			return text[open+1 : state.argumentEnd], text[state.argumentEnd:index], index + 1, nil
		}
	}
	return "", "", 0, errors.New("unterminated call")
}

type pythonArgumentScan struct {
	depth       int
	argumentEnd int
}

func (s *pythonArgumentScan) observe(char byte, index int) bool {
	switch char {
	case '(', '[', '{':
		s.depth++
	case ')':
		if s.depth == 0 {
			if s.argumentEnd < 0 {
				s.argumentEnd = index
			}
			return true
		}
		s.depth--
	case ']', '}':
		if s.depth > 0 {
			s.depth--
		}
	case ',':
		if s.depth == 0 && s.argumentEnd < 0 {
			s.argumentEnd = index
		}
	}
	return false
}

func validPythonPrintTail(tail string) bool {
	tail = strings.TrimSpace(tail)
	if tail == "" {
		return true
	}
	for _, part := range strings.Split(strings.TrimPrefix(tail, ","), ",") {
		part = strings.ReplaceAll(strings.TrimSpace(part), " ", "")
		if part != "file=sys.stderr" && part != `end=""` && part != "end=''" && part != "flush=True" {
			return false
		}
	}
	return true
}

func pythonNotApplicable(text string, callEnd int) string {
	lineEnd := strings.IndexByte(text[callEnd:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text) - callEnd
	}
	comment := strings.TrimSpace(text[callEnd : callEnd+lineEnd])
	switch comment {
	case "# caveman:not-applicable structured-protocol":
		return "structured-protocol"
	case "# caveman:not-applicable untrusted-passthrough":
		return "untrusted-passthrough"
	case "# caveman:not-applicable protocol-marker":
		return "protocol-marker"
	}
	return ""
}

// parsePythonTemplate parses template expressions in Python source strings.
func parsePythonTemplate(expression string) (string, bool, error) {
	parts := splitPythonConcatenation(expression)
	var out strings.Builder
	static := false
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return "", false, errors.New("empty concatenation term")
		}
		value, literal, err := parsePythonLiteralSequence(part)
		if err == nil && literal {
			out.WriteString(value)
			static = true
			continue
		}
		if err != nil && pyliteral.At(part, 0) {
			return "", false, err
		}
		if len(parts) == 1 {
			return "", false, nil
		}
		out.WriteString("{value}")
	}
	return out.String(), static, nil
}

// parsePythonLiteralSequence extracts concatenated string literals from a Python expression.
func parsePythonLiteralSequence(expression string) (string, bool, error) {
	var out strings.Builder
	index := skipPythonSpace(expression, 0)
	found := false
	for index < len(expression) {
		if !pyliteral.At(expression, index) {
			return "", false, nil
		}
		value, end, err := pyliteral.Parse(expression, index)
		if err != nil {
			return "", false, err
		}
		out.WriteString(value)
		found = true
		index = skipPythonSpace(expression, end)
	}
	return out.String(), found, nil
}

// splitPythonConcatenation splits a Python string concatenation into top-level fragments.
func splitPythonConcatenation(expression string) []string {
	parts := []string{}
	start := 0
	depth := 0
	for index := 0; index < len(expression); index++ {
		if pyliteral.At(expression, index) {
			end, err := pyliteral.Skip(expression, index)
			if err != nil {
				return []string{expression}
			}
			index = end - 1
			continue
		}
		switch expression[index] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case '+':
			if depth == 0 {
				parts = append(parts, expression[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, expression[start:])
}

func pythonIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func scanPythonIdentifier(text string, start int) int {
	end := start + 1
	for end < len(text) && (pythonIdentifierStart(text[end]) || text[end] >= '0' && text[end] <= '9') {
		end++
	}
	return end
}

func pythonAttributeCall(text string, start int) bool {
	for index := start - 1; index >= 0 && (text[index] == ' ' || text[index] == '\t'); index-- {
		return text[index] == '.'
	}
	return false
}

func skipPythonSpace(text string, start int) int {
	for start < len(text) && strings.ContainsRune(" \t\r\n", rune(text[start])) {
		start++
	}
	return start
}

func skipPythonHorizontalSpace(text string, start int) int {
	for start < len(text) && (text[start] == ' ' || text[start] == '\t') {
		start++
	}
	return start
}

func skipToNextLine(text string, start int) int {
	if offset := strings.IndexByte(text[start:], '\n'); offset >= 0 {
		return start + offset + 1
	}
	return len(text)
}

func textLineStarts(text string) []int {
	starts := []int{0}
	for index := range text {
		if text[index] == '\n' {
			starts = append(starts, index+1)
		}
	}
	return starts
}

func lineNumber(starts []int, offset int) int {
	return sort.Search(len(starts), func(index int) bool { return starts[index] > offset })
}
