// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// MaxShellFields bounds every consumer of the shared shell tokenizer.
const MaxShellFields = 4096

// ErrShellReadLimit identifies a script whose bounded reader cannot inspect every word.
var ErrShellReadLimit = errors.New("run script exceeds shell reader limit")

// ShellCodeFields reads code words and operators without execution or expansion.
// Quotes and escapes remain literal; comments and escaped newlines are removed.
// Call SplitHereDocs first when script may contain here-document data.
func ShellCodeFields(script string) ([]string, error) {
	_, scan, err := readShellCodeFields(script)
	return scan.fields, err
}

// normalizeShellCode shares comment and continuation handling with all field readers.
func normalizeShellCode(script string) (string, error) {
	script, err := util.StripHashComments(strings.ReplaceAll(script, "\r\n", "\n"))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrShellReadLimit, err)
	}
	return strings.ReplaceAll(script, "\\\n", ""), nil
}

// readShellCodeFields retains word offsets for bounded literal script-file expansion.
func readShellCodeFields(script string) (string, shellCodeReader, error) {
	if len(script) > 1<<20 {
		return "", shellCodeReader{}, fmt.Errorf("%w: byte limit", ErrShellReadLimit)
	}
	script, err := normalizeShellCode(script)
	if err != nil {
		return "", shellCodeReader{}, err
	}
	scan := shellCodeReader{start: -1}
	for i := 0; i < len(script) && i < 1<<20; i++ {
		i = scan.step(script, i)
		if err := scan.check(); err != nil {
			return "", shellCodeReader{}, err
		}
	}
	scan.word(script, len(script))
	if err := scan.check(); err != nil {
		return "", shellCodeReader{}, err
	}
	if scan.stepper.Open() {
		return "", shellCodeReader{}, fmt.Errorf("run script has an unclosed quote")
	}
	return script, scan, nil
}

// check rejects nesting and field overflow without returning partial output.
func (r *shellCodeReader) check() error {
	if r.stepper.ExceededLimit() {
		return fmt.Errorf("%w: nesting exceeds shell reader limit", ErrShellReadLimit)
	}
	if len(r.fields) > MaxShellFields {
		return fmt.Errorf("%w: %d fields", ErrShellReadLimit, MaxShellFields)
	}
	return nil
}

type shellCodeReader struct {
	fields  []string
	starts  []int
	start   int
	stepper QuoteStepper
}

// step reads one byte or quoted span without treating quoted operators as separators.
func (r *shellCodeReader) step(script string, i int) int {
	c := script[i]
	if r.stepper.InQuote() || strings.ContainsRune("'\"\\", rune(c)) {
		if r.start < 0 {
			r.start = i
		}
		return r.stepper.Step(script, i)
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

// operator emits a shell separator, collapsing consecutive newline separators.
func (r *shellCodeReader) operator(script string, i int) int {
	r.word(script, i)
	op := string(script[i])
	if op == "\n" {
		if len(r.fields) > 0 && r.fields[len(r.fields)-1] == ";" {
			return i
		}
		op = ";"
	} else if (op == "&" || op == "|") && i+1 < len(script) && script[i+1] == script[i] {
		i++
		op += op
	}
	r.fields = append(r.fields, op)
	r.starts = append(r.starts, i-len(op)+1)
	return i
}

// word emits a pending word with its original normalized-code offset.
func (r *shellCodeReader) word(script string, end int) {
	if r.start >= 0 {
		r.fields = append(r.fields, script[r.start:end])
		r.starts = append(r.starts, r.start)
		r.start = -1
	}
}
