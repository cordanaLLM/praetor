package cavemansource

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type shellWord struct {
	value  string
	quoted bool
}

func extractShell(item discoveredInput, text string) ([]Source, error) {
	lines := strings.Split(normalizeNewlines(text), "\n")
	sources := []Source{}
	for index := 0; index < len(lines); index++ {
		source, end, err := extractShellLine(item, lines, index)
		if err != nil {
			return nil, err
		}
		if source != nil {
			source.Selector = fmt.Sprintf("shell-output:%03d", len(sources))
			sources = append(sources, *source)
		}
		index = end
	}
	return sources, nil
}

func extractShellLine(item discoveredInput, lines []string, index int) (*Source, int, error) {
	line := strings.TrimSpace(lines[index])
	if line == "" || strings.HasPrefix(line, "#") {
		return nil, index, nil
	}
	if marker := shellOperatorIndex(line, "<<"); marker >= 0 {
		value, end, err := extractHereDoc(lines, index, line, marker)
		if err != nil {
			return nil, 0, fmt.Errorf("caveman source %s line %d: heredoc output unverified: %w", item.path, index+1, err)
		}
		source := sourceFrom(item, value, fmt.Sprintf("heredoc:%d", index+1), index+2)
		return &source, end, nil
	}
	command, args, ok := shellOutputCommand(line)
	if !ok {
		return unpositionedShellOutput(item, line, index)
	}
	value, err := extractShellCommand(command, args)
	if err != nil {
		return nil, 0, fmt.Errorf("caveman source %s line %d: %s output unverified: %w", item.path, index+1, command, err)
	}
	source := sourceFrom(item, value, fmt.Sprintf("%s:%d", command, index+1), index+1)
	return &source, index, nil
}

func unpositionedShellOutput(item discoveredInput, line string, index int) (*Source, int, error) {
	command := shellOutputToken(line)
	if command == "" {
		return nil, index, nil
	}
	return nil, 0, fmt.Errorf("caveman source %s line %d: %s output unverified: command position unsupported",
		item.path, index+1, command)
}

func shellOutputCommand(line string) (string, string, bool) {
	for _, command := range []string{"echo", "printf"} {
		if line == command {
			return command, "", true
		}
		if strings.HasPrefix(line, command+" ") || strings.HasPrefix(line, command+"\t") {
			return command, strings.TrimSpace(line[len(command):]), true
		}
	}
	return "", "", false
}

func shellOutputToken(line string) string {
	for index := 0; index < len(line); {
		if line[index] == '#' {
			return ""
		}
		if line[index] == '\'' || line[index] == '"' {
			index = skipShellQuoted(line, index)
			continue
		}
		if line[index] == '\\' {
			index += 2
			continue
		}
		if !shellNameByte(line[index]) {
			index++
			continue
		}
		end := index + 1
		for end < len(line) && shellNameByte(line[end]) {
			end++
		}
		if token := line[index:end]; token == "echo" || token == "printf" {
			return token
		}
		index = end
	}
	return ""
}

func shellNameByte(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func skipShellQuoted(line string, start int) int {
	quote := line[start]
	for index := start + 1; index < len(line); index++ {
		if quote == '"' && line[index] == '\\' {
			index++
			continue
		}
		if line[index] == quote {
			return index + 1
		}
	}
	return len(line)
}

func shellOperatorIndex(line, operator string) int {
	for index := 0; index+len(operator) <= len(line); index++ {
		if line[index] == '#' {
			return -1
		}
		if line[index] == '\'' || line[index] == '"' {
			index = skipShellQuoted(line, index) - 1
			continue
		}
		if line[index] == '\\' {
			index++
			continue
		}
		if strings.HasPrefix(line[index:], operator) {
			return index
		}
	}
	return -1
}

func extractShellCommand(command, args string) (string, error) {
	args = stripShellStderrRedirection(args)
	words, err := parseShellWords(args)
	if err != nil {
		return "", err
	}
	if command == "echo" {
		return renderEcho(words)
	}
	return renderPrintf(words)
}

// stripShellStderrRedirection removes only the two static stdout-to-stderr forms used by
// hook messages. Other shell operators remain in the argument parser and fail closed.
func stripShellStderrRedirection(args string) string {
	trimmed := strings.TrimSpace(args)
	for _, redirect := range []string{"1>&2", ">&2"} {
		if strings.HasSuffix(trimmed, redirect) {
			return strings.TrimSpace(strings.TrimSuffix(trimmed, redirect))
		}
	}
	return trimmed
}

func renderEcho(words []shellWord) (string, error) {
	newline := true
	if len(words) > 0 && words[0].value == "-n" && !words[0].quoted {
		newline = false
		words = words[1:]
	}
	if len(words) == 0 {
		return "", errors.New("empty output")
	}
	values := make([]string, len(words))
	for index := range words {
		if !words[index].quoted {
			return "", errors.New("unquoted or computed arguments are unsupported")
		}
		values[index] = words[index].value
	}
	value := strings.Join(values, " ")
	if newline {
		value += "\n"
	}
	return value, nil
}

func renderPrintf(words []shellWord) (string, error) {
	if len(words) == 0 || !words[0].quoted {
		return "", errors.New("static quoted format required")
	}
	for index := 1; index < len(words); index++ {
		if !words[index].quoted {
			return "", errors.New("unquoted or computed arguments are unsupported")
		}
	}
	format, err := decodePrintfEscapes(words[0].value)
	if err != nil {
		return "", err
	}
	return substitutePrintf(format, words[1:])
}

func substitutePrintf(format string, args []shellWord) (string, error) {
	var out strings.Builder
	arg := 0
	for index := 0; index < len(format); index++ {
		if format[index] != '%' {
			out.WriteByte(format[index])
			continue
		}
		if index+1 >= len(format) {
			return "", errors.New("trailing printf percent")
		}
		index++
		if format[index] == '%' {
			out.WriteByte('%')
			continue
		}
		if format[index] != 's' || arg >= len(args) {
			return "", errors.New("only bounded %s substitution is supported")
		}
		out.WriteString(args[arg].value)
		arg++
	}
	if arg != len(args) {
		return "", errors.New("printf argument count does not match format")
	}
	return out.String(), nil
}

func decodePrintfEscapes(value string) (string, error) {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			out.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "", errors.New("trailing printf escape")
		}
		index++
		consumed, err := appendPrintfEscape(&out, value[index:])
		if err != nil {
			return "", err
		}
		index += consumed - 1
	}
	return out.String(), nil
}

func appendPrintfEscape(out *strings.Builder, value string) (int, error) {
	switch value[0] {
	case 'e', 'E':
		out.WriteByte(0x1b)
		return 1, nil
	case 'c':
		return 0, errors.New("printf \\c early termination is unsupported")
	case '0':
		return appendPrintfOctal(out, value)
	default:
		return appendPythonEscape(out, value)
	}
}

func appendPrintfOctal(out *strings.Builder, value string) (int, error) {
	length := 1
	for length < len(value) && length < 4 && value[length] >= '0' && value[length] <= '7' {
		length++
	}
	number, err := strconv.ParseUint(value[:length], 8, 8)
	if err != nil {
		return 0, errors.New("invalid printf octal escape")
	}
	out.WriteByte(byte(number))
	return length, nil
}

func parseShellWords(value string) ([]shellWord, error) {
	words := []shellWord{}
	for index := 0; index < len(value); {
		for index < len(value) && (value[index] == ' ' || value[index] == '\t') {
			index++
		}
		if index >= len(value) || value[index] == '#' {
			break
		}
		word, end, err := parseShellWord(value, index)
		if err != nil {
			return nil, err
		}
		words = append(words, word)
		index = end
	}
	return words, nil
}

func parseShellWord(value string, start int) (shellWord, int, error) {
	if value[start] == '\'' || value[start] == '"' {
		return parseQuotedShellWord(value, start)
	}
	end := start
	for end < len(value) && value[end] != ' ' && value[end] != '\t' {
		end++
	}
	raw := value[start:end]
	if strings.ContainsAny(raw, "$`*?[]{};|&<>") {
		return shellWord{}, 0, errors.New("computed or shell-active argument is unsupported")
	}
	return shellWord{value: raw}, end, nil
}

func parseQuotedShellWord(value string, start int) (shellWord, int, error) {
	if value[start] == '\'' {
		return parseSingleQuotedShellWord(value, start)
	}
	return parseDoubleQuotedShellWord(value, start)
}

func parseSingleQuotedShellWord(value string, start int) (shellWord, int, error) {
	var out strings.Builder
	for index := start + 1; index < len(value); index++ {
		if value[index] == '\'' {
			return finishQuotedShellWord(value, index, out.String())
		}
		out.WriteByte(value[index])
	}
	return shellWord{}, 0, errors.New("unterminated shell string")
}

func parseDoubleQuotedShellWord(value string, start int) (shellWord, int, error) {
	var out strings.Builder
	for index := start + 1; index < len(value); index++ {
		switch value[index] {
		case '"':
			return finishQuotedShellWord(value, index, out.String())
		case '\\':
			next, err := appendDoubleShellEscape(&out, value, index)
			if err != nil {
				return shellWord{}, 0, err
			}
			index = next
		case '$', '`':
			return shellWord{}, 0, errors.New("interpolated shell string is unsupported")
		default:
			out.WriteByte(value[index])
		}
	}
	return shellWord{}, 0, errors.New("unterminated shell string")
}

func appendDoubleShellEscape(out *strings.Builder, value string, index int) (int, error) {
	if index+1 >= len(value) {
		return 0, errors.New("trailing shell escape")
	}
	next := value[index+1]
	if strings.ContainsRune("$`\"\\", rune(next)) {
		out.WriteByte(next)
		return index + 1, nil
	}
	out.WriteByte('\\')
	return index, nil
}

func finishQuotedShellWord(value string, end int, decoded string) (shellWord, int, error) {
	if end+1 < len(value) && value[end+1] != ' ' && value[end+1] != '\t' && value[end+1] != '#' {
		return shellWord{}, 0, errors.New("concatenated shell words are unsupported")
	}
	return shellWord{value: decoded, quoted: true}, end + 1, nil
}

func extractHereDoc(lines []string, start int, declaration string, marker int) (string, int, error) {
	token := strings.TrimSpace(declaration[marker+2:])
	stripTabs := strings.HasPrefix(token, "-")
	token = strings.TrimSpace(strings.TrimPrefix(token, "-"))
	delimiter, quoted, err := hereDocDelimiter(token)
	if err != nil {
		return "", start, err
	}
	body := []string{}
	for index := start + 1; index < len(lines); index++ {
		line := lines[index]
		compare := line
		if stripTabs {
			compare = strings.TrimLeft(compare, "\t")
		}
		if compare == delimiter {
			text := strings.Join(body, "\n") + "\n"
			if !quoted && strings.ContainsAny(text, "$`\\") {
				return "", start, errors.New("interpolated heredoc is unsupported")
			}
			return text, index, nil
		}
		body = append(body, line)
	}
	return "", start, errors.New("unterminated heredoc")
}

func hereDocDelimiter(token string) (string, bool, error) {
	if token == "" {
		return "", false, errors.New("missing heredoc delimiter")
	}
	end := strings.IndexAny(token, " \t")
	if end >= 0 {
		token = token[:end]
	}
	if len(token) >= 2 && (token[0] == '\'' && token[len(token)-1] == '\'' || token[0] == '"' && token[len(token)-1] == '"') {
		return token[1 : len(token)-1], true, nil
	}
	if strings.ContainsAny(token, "$`'\"") {
		return "", false, errors.New("unsupported heredoc delimiter")
	}
	return token, false, nil
}
