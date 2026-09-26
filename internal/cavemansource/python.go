package cavemansource

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
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

func scanPythonSource(item discoveredInput, text string, starts []int, index int) (int, *Source, error) {
	switch {
	case text[index] == '#':
		return skipToNextLine(text, index), nil, nil
	case text[index] == '\'' || text[index] == '"':
		_, end, err := parsePythonString(text, index)
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

func pythonFirstArgument(text string, open int) (string, string, int, error) {
	state := pythonArgumentScan{argumentEnd: -1}
	for index := open + 1; index < len(text); index++ {
		if pythonStringAt(text, index) {
			end, err := skipPythonString(text, index)
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
		if err != nil && pythonStringAt(part, 0) {
			return "", false, err
		}
		if len(parts) == 1 {
			return "", false, nil
		}
		out.WriteString("{value}")
	}
	return out.String(), static, nil
}

func parsePythonLiteralSequence(expression string) (string, bool, error) {
	var out strings.Builder
	index := skipPythonSpace(expression, 0)
	found := false
	for index < len(expression) {
		if !pythonStringAt(expression, index) {
			return "", false, nil
		}
		value, end, err := parsePythonString(expression, index)
		if err != nil {
			return "", false, err
		}
		out.WriteString(value)
		found = true
		index = skipPythonSpace(expression, end)
	}
	return out.String(), found, nil
}

func splitPythonConcatenation(expression string) []string {
	parts := []string{}
	start := 0
	depth := 0
	for index := 0; index < len(expression); index++ {
		if pythonStringAt(expression, index) {
			end, err := skipPythonString(expression, index)
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

func parsePythonString(text string, start int) (string, int, error) {
	prefix, index, err := scanPythonStringPrefix(text, start)
	if err != nil {
		return "", 0, err
	}
	quote, width, err := pythonStringDelimiter(text, index)
	if err != nil {
		return "", 0, err
	}
	bodyStart := index + width
	bodyEnd, err := findPythonStringEnd(text, bodyStart, quote, width)
	if err != nil {
		return "", 0, err
	}
	body := text[bodyStart:bodyEnd]
	if strings.Contains(prefix, "r") {
		if strings.Contains(prefix, "f") {
			value, _, decodeErr := decodePythonFString(body, true)
			return value, bodyEnd + width, decodeErr
		}
		return body, bodyEnd + width, nil
	}
	if strings.Contains(prefix, "f") {
		value, _, decodeErr := decodePythonFString(body, false)
		return value, bodyEnd + width, decodeErr
	}
	decoded, err := decodePythonEscapes(body)
	return decoded, bodyEnd + width, err
}

// supportedPythonStringPrefixes are the lower-cased prefixes whose literal decodes to text.
var supportedPythonStringPrefixes = map[string]bool{"": true, "r": true, "u": true, "f": true, "fr": true, "rf": true}

func scanPythonStringPrefix(text string, start int) (string, int, error) {
	index := start
	for index < len(text) && index-start < 3 && pythonPrefix(text[index]) {
		index++
	}
	prefix := strings.ToLower(text[start:index])
	if strings.Contains(prefix, "b") {
		return "", 0, errors.New("byte strings are unsupported")
	}
	if !supportedPythonStringPrefixes[prefix] {
		return "", 0, errors.New("unsupported Python string prefix")
	}
	return prefix, index, nil
}

func decodePythonFString(body string, raw bool) (string, int, error) {
	var out strings.Builder
	for index := 0; index < len(body); {
		next, err := appendPythonFStringPart(&out, body, index, raw)
		if err != nil {
			return "", 0, err
		}
		index = next
	}
	return out.String(), len(body), nil
}

func appendPythonFStringPart(out *strings.Builder, body string, index int, raw bool) (int, error) {
	switch body[index] {
	case '{':
		return appendPythonFStringOpen(out, body, index)
	case '}':
		if index+1 < len(body) && body[index+1] == '}' {
			out.WriteByte('}')
			return index + 2, nil
		}
		return 0, errors.New("unmatched formatted string brace")
	default:
		return appendPythonFStringText(out, body, index, raw)
	}
}

func appendPythonFStringOpen(out *strings.Builder, body string, index int) (int, error) {
	if index+1 < len(body) && body[index+1] == '{' {
		out.WriteByte('{')
		return index + 2, nil
	}
	end, err := findPythonFExpressionEnd(body, index+1)
	if err != nil {
		return 0, err
	}
	out.WriteString("{value}")
	return end + 1, nil
}

func appendPythonFStringText(out *strings.Builder, body string, index int, raw bool) (int, error) {
	start := index
	for index < len(body) && body[index] != '{' && body[index] != '}' {
		index++
	}
	segment := body[start:index]
	if raw {
		out.WriteString(segment)
		return index, nil
	}
	decoded, err := decodePythonEscapes(segment)
	if err != nil {
		return 0, err
	}
	out.WriteString(decoded)
	return index, nil
}

func findPythonFExpressionEnd(body string, start int) (int, error) {
	depth := 0
	for index := start; index < len(body); index++ {
		if body[index] == '{' {
			depth++
			continue
		}
		if body[index] != '}' {
			continue
		}
		if depth == 0 {
			return index, nil
		}
		depth--
	}
	return 0, errors.New("unterminated formatted string expression")
}

func pythonStringAt(text string, index int) bool {
	if index >= len(text) {
		return false
	}
	if text[index] == '\'' || text[index] == '"' {
		return true
	}
	if !pythonPrefix(text[index]) {
		return false
	}
	_, quote, err := scanPythonStringPrefix(text, index)
	return err == nil && quote < len(text) && (text[quote] == '\'' || text[quote] == '"')
}

func skipPythonString(text string, start int) (int, error) {
	_, index, err := scanPythonStringPrefix(text, start)
	if err != nil {
		return 0, err
	}
	quote, width, err := pythonStringDelimiter(text, index)
	if err != nil {
		return 0, err
	}
	end, err := findPythonStringEnd(text, index+width, quote, width)
	if err != nil {
		return 0, err
	}
	return end + width, nil
}

func pythonStringDelimiter(text string, index int) (byte, int, error) {
	if index >= len(text) || (text[index] != '\'' && text[index] != '"') {
		return 0, 0, errors.New("expected Python string literal")
	}
	quote := text[index]
	if index+2 < len(text) && text[index+1] == quote && text[index+2] == quote {
		return quote, 3, nil
	}
	return quote, 1, nil
}

func findPythonStringEnd(text string, start int, quote byte, width int) (int, error) {
	for index := start; index < len(text); index++ {
		if text[index] == '\\' {
			index++
			continue
		}
		if text[index] != quote {
			continue
		}
		if width == 1 || index+2 < len(text) && text[index+1] == quote && text[index+2] == quote {
			return index, nil
		}
	}
	return 0, errors.New("unterminated Python string literal")
}

func decodePythonEscapes(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", errors.New("trailing Python escape")
		}
		index++
		consumed, err := appendPythonEscape(&out, value[index:])
		if err != nil {
			return "", err
		}
		index += consumed - 1
	}
	return out.String(), nil
}

func appendPythonEscape(out *strings.Builder, value string) (int, error) {
	simple := map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v', '\\': '\\', '\'': '\'', '"': '"'}
	if decoded, ok := simple[value[0]]; ok {
		out.WriteByte(decoded)
		return 1, nil
	}
	if value[0] == '\n' {
		return 1, nil
	}
	if value[0] == 'x' {
		return appendPythonHex(out, value, 2)
	}
	if value[0] == 'u' {
		return appendPythonHex(out, value, 4)
	}
	if value[0] == 'U' {
		return appendPythonHex(out, value, 8)
	}
	if value[0] == 'N' {
		return 0, errors.New("named Python Unicode escapes are unsupported")
	}
	if value[0] >= '0' && value[0] <= '7' {
		return appendPythonOctal(out, value)
	}
	out.WriteByte('\\')
	out.WriteByte(value[0])
	return 1, nil
}

func appendPythonHex(out *strings.Builder, value string, digits int) (int, error) {
	if len(value) < digits+1 {
		return 0, errors.New("short Python hexadecimal escape")
	}
	var decoded rune
	_, err := fmt.Sscanf(value[1:digits+1], "%x", &decoded)
	if err != nil || !utf8.ValidRune(decoded) {
		return 0, errors.New("invalid Python hexadecimal escape")
	}
	out.WriteRune(decoded)
	return digits + 1, nil
}

func appendPythonOctal(out *strings.Builder, value string) (int, error) {
	length := 1
	for length < len(value) && length < 3 && value[length] >= '0' && value[length] <= '7' {
		length++
	}
	number, err := strconv.ParseUint(value[:length], 8, 8)
	if err != nil {
		return 0, errors.New("invalid Python octal escape")
	}
	out.WriteByte(byte(number))
	return length, nil
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

func pythonPrefix(value byte) bool {
	return strings.ContainsRune("rRuUbBfF", rune(value))
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
