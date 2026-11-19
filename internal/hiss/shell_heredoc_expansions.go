// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"strings"
)

// SubstitutionCode extracts executable expansions without reading data prose as shell code.
// Quoting the delimiter disables expansion, including backticks; nesting uses a bounded queue.
func (h HereDoc) SubstitutionCode() (string, error) {
	if h.quoted {
		return "", nil
	}
	pending := []hereDocFragment{{h.Text, true}}
	var code strings.Builder
	for i := 0; i < len(pending) && i < MaxShellFields; i++ {
		parts, err := hereDocReadFragment(pending[i])
		if err != nil {
			return "", err
		}
		for j := 0; j < len(parts) && j < MaxShellFields; j++ {
			if code.Len()+len(parts[j])+8 > 1<<20 || len(pending) >= MaxShellFields {
				return "", fmt.Errorf("here-document substitution exceeds reader limit")
			}
			code.WriteString("\n(\n" + parts[j] + "\n)\n")
			pending = append(pending, hereDocFragment{parts[j], false})
		}
	}
	return code.String(), nil
}

// hereDocSubstitutions preserves argument quotes inside expansions and literal quotes in data.
func hereDocSubstitutions(text string, data bool) ([]string, error) {
	text, err := hereDocSubstitutionText(text, data)
	if err != nil {
		return nil, err
	}
	var parts []string
	var quote QuoteStepper
	for i := 0; i < len(text) && i < maxHereDocLineBytes; i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		width := hereDocExpansionAt(text[i:], data, quote.OpenQuote())
		if width > 0 {
			body, end, err := hereDocSubstitution(text, i, width)
			if err != nil {
				return nil, err
			}
			parts = append(parts, body)
			i = end
			continue
		}
		if !data {
			i = quote.Step(text, i)
		}
	}
	return parts, nil
}

// hereDocSubstitutionWidth names executable substitution openers, excluding arithmetic syntax.
func hereDocSubstitutionWidth(text string) int {
	if strings.HasPrefix(text, "$(") && !strings.HasPrefix(text, "$((") {
		return 2
	}
	if strings.HasPrefix(text, "`") {
		return 1
	}
	return 0
}

// hereDocSubstitution finds one matching terminator with the shared bounded quote reader.
func hereDocSubstitution(text string, start, width int) (string, int, error) {
	var quote QuoteStepper
	if width == 2 {
		quote.push(shellCommand)
	} else {
		quote.push(shellBacktick)
	}
	for i := start + width; i < len(text) && i < maxHereDocLineBytes; i++ {
		next := quote.Step(text, i)
		if quote.ExceededLimit() {
			return "", 0, fmt.Errorf("here-document substitution exceeds nesting limit")
		}
		if !quote.Open() {
			return text[start+width : i], i, nil
		}
		i = next
	}
	return "", 0, fmt.Errorf("unproven unterminated here-document substitution")
}

// hereDocInsertion retains a substitution's lexical owner boundary before offset adjustment.
type hereDocInsertion struct {
	at   int
	code string
}

// hereDocExpansionCode inserts expansions before their owner, preserving directory and child scope.
func hereDocExpansionCode(code string, docs []HereDoc) (string, error) {
	var out strings.Builder
	var inserts []hereDocInsertion
	written := 0
	for i := 0; i < len(docs) && i < maxShellHeredocs; i++ {
		expansion, err := docs[i].SubstitutionCode()
		if err != nil {
			return "", err
		}
		if expansion == "" {
			continue
		}
		if docs[i].nested {
			return "", fmt.Errorf("unproven nested here-document expansion owner")
		}
		body := "\n(\n" + expansion + "\n)\n"
		at := docs[i].codeStart
		if at < written || at > len(code) {
			return "", fmt.Errorf("unproven overlapping here-document owners")
		}
		out.WriteString(code[written:at])
		out.WriteString(body)
		written = at
		inserts = append(inserts, hereDocInsertion{at, body})
		if out.Len()+len(code)-written > 1<<20 {
			return "", fmt.Errorf("here-document expanded code exceeds byte limit")
		}
	}
	out.WriteString(code[written:])
	hereDocShiftOffsets(docs, inserts)
	return out.String(), nil
}

// hereDocShiftOffsets maps definition boundaries past inserted substitution code.
func hereDocShiftOffsets(docs []HereDoc, inserts []hereDocInsertion) {
	for i := 0; i < len(docs) && i < maxShellHeredocs; i++ {
		original := docs[i].offset
		for j := 0; j < len(inserts) && j < maxShellHeredocs; j++ {
			if inserts[j].at <= original {
				docs[i].offset += len(inserts[j].code)
			}
		}
	}
}

// hereDocExpansionAt keeps single-quoted shell operands literal while data quotes remain prose.
func hereDocExpansionAt(text string, data bool, quote byte) int {
	if !data && quote == '\'' {
		return 0
	}
	return hereDocSubstitutionWidth(text)
}

// hereDocSubstitutionText removes comments only inside executable shell fragments.
func hereDocSubstitutionText(text string, data bool) (string, error) {
	if data {
		return text, nil
	}
	return normalizeShellCode(text)
}

// hereDocFragment distinguishes data quotes from quotes within executable substitutions.
type hereDocFragment struct {
	text string
	data bool
}

// hereDocReadFragment declines nested substitutions whose enclosing directory cannot be retained.
func hereDocReadFragment(fragment hereDocFragment) ([]string, error) {
	parts, err := hereDocSubstitutions(fragment.text, fragment.data)
	if err != nil {
		return nil, err
	}
	if !fragment.data && len(parts) > 0 && hereDocChangesDirectory(fragment.text) {
		return nil, fmt.Errorf("unproven nested substitution directory state")
	}
	return parts, nil
}

// hereDocChangesDirectory names parent-state commands before flattening a nested substitution.
func hereDocChangesDirectory(text string) bool {
	fields, err := ShellCodeFields(text)
	if err != nil {
		return true
	}
	for i := 0; i < len(fields) && i < MaxShellFields; i++ {
		switch literalHereDocWord(fields[i]) {
		case "cd", "pushd", "popd", ".", "source", "eval":
			return true
		}
	}
	return false
}
