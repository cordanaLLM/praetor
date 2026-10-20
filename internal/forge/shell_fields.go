package forge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrShellReadLimit identifies a script whose bounded reader cannot inspect every word.
var ErrShellReadLimit = errors.New("run script exceeds shell reader limit")

// ShellFields reads shell words and control operators without executing or expanding them.
// Quotes and escapes stay in words; quoted whitespace and operators stay literal. Comments
// are removed, backslash-newline continues a command, and an unquoted newline becomes ";".
// Operators ;, &, &&, | and || are separate fields even without surrounding whitespace.
// The forge's shellWords/parseShellCommand readers consume these same fields. More than
// maxRunScriptFields fields or an unclosed quote is an error, never a partial result.
func ShellFields(script string) ([]string, error) {
	script, err := util.StripHashComments(strings.ReplaceAll(script, "\r\n", "\n"))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrShellReadLimit, err)
	}
	script = strings.ReplaceAll(script, "\\\n", "")
	scan := shellFieldReader{start: -1}
	for i := 0; i < len(script); i++ {
		i = scan.step(script, i)
		if len(scan.fields) > maxRunScriptFields {
			return nil, fmt.Errorf("%w: %d fields", ErrShellReadLimit, maxRunScriptFields)
		}
	}
	scan.word(script, len(script))
	if scan.quote != 0 {
		return nil, fmt.Errorf("run script has an unclosed quote")
	}
	if len(scan.fields) > maxRunScriptFields {
		return nil, fmt.Errorf("%w: %d fields", ErrShellReadLimit, maxRunScriptFields)
	}
	return scan.fields, nil
}

type shellFieldReader struct {
	fields []string
	start  int
	quote  byte
}

func (r *shellFieldReader) step(script string, i int) int {
	c := script[i]
	if r.quote != 0 || strings.ContainsRune("'\"\\", rune(c)) {
		if r.start < 0 {
			r.start = i
		}
		i, r.quote = shellQuoteStep(script, i, r.quote)
		return i
	}
	if strings.ContainsRune(" \t\r\v\f", rune(c)) {
		r.word(script, i)
		return i
	}
	if strings.ContainsRune(";&|\n", rune(c)) {
		return r.operator(script, i)
	}
	if r.start < 0 {
		r.start = i
	}
	return i
}

func (r *shellFieldReader) operator(script string, i int) int {
	r.word(script, i)
	op := string(script[i])
	if op == "\n" {
		op = ";"
	} else if (op == "&" || op == "|") && i+1 < len(script) && script[i+1] == script[i] {
		i++
		op += op
	}
	r.fields = append(r.fields, op)
	return i
}

func (r *shellFieldReader) word(script string, end int) {
	if r.start >= 0 {
		r.fields = append(r.fields, script[r.start:end])
		r.start = -1
	}
}
