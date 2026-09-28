package adopt

import (
	"math"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Layout of the two hook files adoption renders from templates (hooks.go). Adopters lint
// their whole tree, so both pass the defaults scripts/test_emitted_hook_lint.py applies to
// the fixtures under testdata/emitted: yamllint --strict with its default configuration for
// lefthook.yml, black and flake8 at 100 columns for block_evasion.py (BUG-782). The helpers
// below lay generated values out the way those tools leave them untouched.
const (
	// yamlLineLimit is the line-length maximum of yamllint's default configuration, the one
	// util.FitYAMLLines keeps the manifest and lock within.
	yamlLineLimit = util.YAMLLineLimit
	// yamlRunKey is a lefthook job's run key at the indentation buildLefthookYAMLFor uses.
	yamlRunKey = "      run: "
	// yamlFoldIndent indents the lines of a folded run value one level below its key.
	yamlFoldIndent = "        "
	// blackLineLimit is black's default line length.
	blackLineLimit = 88
	// maxLayoutUnits bounds the words or literal units one value is laid out from (HISS-02).
	maxLayoutUnits = 1 << 16
)

// lefthookRun renders one run key. A command that fits stays one plain scalar; a longer one
// becomes a folded block scalar (>-) broken at single spaces, which YAML joins back into
// exactly the one-line command, so the value lefthook runs and lefthook_identity.go reads
// is unchanged. Text that folding cannot reproduce stays one line.
func lefthookRun(command string) string {
	if len(yamlRunKey)+len(command) <= yamlLineLimit {
		return yamlRunKey + command + "\n"
	}
	lines, ok := foldAtSpaces(command, yamlLineLimit-len(yamlFoldIndent))
	if !ok {
		return yamlRunKey + command + "\n"
	}
	return yamlRunKey + ">-\n" + yamlFoldIndent + strings.Join(lines, "\n"+yamlFoldIndent) + "\n"
}

// foldAtSpaces breaks text at single spaces into lines of at most width bytes; a word longer
// than width keeps a line of its own. It reports false for text a folded scalar would not
// read back: folding joins lines with exactly one space and keeps a line that starts with
// white space verbatim, so a tab, a line break, a leading or trailing space or a run of
// spaces is not folded.
func foldAtSpaces(text string, width int) ([]string, bool) {
	if strings.ContainsAny(text, "\t\r\n") {
		return nil, false
	}
	words := strings.Split(text, " ")
	if len(words) > maxLayoutUnits || slices.Contains(words, "") {
		return nil, false
	}
	lines := []string{words[0]}
	for _, word := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(word) > width {
			lines = append(lines, word)
			continue
		}
		lines[last] += " " + word
	}
	return lines, true
}

// pyKind is how the interceptor template embeds one value.
type pyKind int

const (
	// pyString is an escaped string literal.
	pyString pyKind = iota
	// pyRaw is a raw string literal; its text must hold no double quote.
	pyRaw
	// pyName is a bare Python name, never split.
	pyName
)

// pyToken is one value of the interceptor template.
type pyToken struct {
	text string
	kind pyKind
}

// whole renders the token as one Python token.
func (t pyToken) whole() string {
	return t.literals(math.MaxInt)[0]
}

// literals renders the token as adjacent literals of at most width bytes each; a name stays
// whole. black keeps adjacent literals one per line when they do not fit on one line
// together, and joins them when they do.
func (t pyToken) literals(width int) []string {
	if t.kind == pyName {
		return []string{t.text}
	}
	open := `"`
	if t.kind == pyRaw {
		open = `r"`
	}
	units := pythonLiteralUnits(t.text, t.kind == pyRaw)
	room := max(width-len(open)-1, 2)
	var literals []string
	for start := 0; start < len(units); {
		end := literalEnd(units, start, room, t.kind == pyString)
		literals = append(literals, open+strings.Join(units[start:end], "")+`"`)
		start = end
	}
	if len(literals) == 0 {
		literals = []string{open + `"`}
	}
	return literals
}

// pythonLiteralUnits splits text into the smallest pieces a literal may end between. An
// escaped string escapes each rune on its own; a raw string keeps a backslash with the byte
// after it, so no raw literal ends in a backslash, which would swallow its closing quote.
func pythonLiteralUnits(text string, raw bool) []string {
	units := make([]string, 0, min(len(text), maxLayoutUnits))
	if !raw {
		for _, r := range text {
			units = append(units, pythonEscape(r))
		}
		return units
	}
	for i := 0; i < len(text) && len(units) < maxLayoutUnits; i++ {
		if text[i] == '\\' && i+1 < len(text) {
			units = append(units, text[i:i+2])
			i++
			continue
		}
		units = append(units, text[i:i+1])
	}
	return units
}

// pythonEscape renders one rune inside a double-quoted Python string literal.
func pythonEscape(r rune) string {
	switch r {
	case '\\':
		return `\\`
	case '"':
		return `\"`
	case '\n':
		return `\n`
	}
	return string(r)
}

// literalEnd returns the end of the literal starting at units[start]: as many units as fit
// room bytes, at least one. An escaped string breaks after its last space when it has one.
func literalEnd(units []string, start, room int, words bool) int {
	end, size := start, 0
	for end < len(units) && (end == start || size+len(units[end]) <= room) {
		size += len(units[end])
		end++
	}
	if !words || end == len(units) {
		return end
	}
	for i := end - 1; i > start; i-- {
		if units[i] == " " {
			return i + 1
		}
	}
	return end
}

// pythonPairEntry lays out one two-item tuple of a list at four-space indentation the way
// black does: one line when it fits, otherwise one item per line with a trailing comma.
func pythonPairEntry(first, second pyToken) string {
	if line := "    (" + first.whole() + ", " + second.whole() + "),"; len(line) <= blackLineLimit {
		return line + "\n"
	}
	return "    (\n" + pythonItemLines("        ", first, ",") + pythonItemLines("        ", second, ",") + "    ),\n"
}

// pythonItemLines renders one item at indent followed by suffix: one line when it fits,
// otherwise adjacent literals of one line each.
func pythonItemLines(indent string, item pyToken, suffix string) string {
	if line := indent + item.whole() + suffix; len(line) <= blackLineLimit || item.kind == pyName {
		return line + "\n"
	}
	literals := item.literals(blackLineLimit - len(indent) - len(suffix))
	return indent + strings.Join(literals, "\n"+indent) + suffix + "\n"
}

// pythonAssignment lays out a module-level NAME = value. A value too long for one line is
// wrapped in parentheses as adjacent literals, one per line. A value that fits one line
// inside the parentheses is split in two literals kept on that line: black removes the
// parentheses around a single literal, leaving a line past flake8's limit, and joins
// literals that fit one line together.
func pythonAssignment(name string, value pyToken) string {
	if line := name + " = " + value.whole(); len(line) <= blackLineLimit || value.kind == pyName {
		return line + "\n"
	}
	const indent = "    "
	separator := "\n" + indent
	literals := value.literals(blackLineLimit - len(indent))
	if len(literals) == 1 {
		literals = value.literals(len(literals[0])/2 + 3)
		separator = " "
	}
	return name + " = (\n" + indent + strings.Join(literals, separator) + "\n)\n"
}
