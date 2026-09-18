package caveman

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// ansiRe matches CSI sequences (colours, cursor moves), OSC sequences (titles,
	// hyperlinks) and the two-byte escapes.
	ansiRe     = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
	spaceRunRe = regexp.MustCompile(`[ \t]{2,}`)
)

// Stats measures one Compress call. Token figures come from EstimateTokens.
type Stats struct {
	BytesIn, BytesOut         int
	TokensEstIn, TokensEstOut int
}

// Compress applies the cleanups that cannot change meaning, and nothing else:
//
//   - ANSI escape sequences are removed everywhere;
//   - CRLF line ends become LF;
//   - in prose lines, trailing blanks go and inner runs of blanks outside code spans
//     collapse to one space, leading indentation kept;
//   - a run of blank lines becomes one blank line;
//   - identical consecutive prose lines fold into one line ending in " (xN)".
//
// Fenced code, caveman:off regions and structured lines (headings, tables, HTML comments,
// ledger rows, hook protocol lines, evidence pointers) keep their bytes apart from ANSI and
// CRLF. Prose is never rewritten: no word is dropped or replaced.
func Compress(text string) (string, Stats) {
	lines, _ := scan(ansiRe.ReplaceAllString(text, ""))
	out := make([]string, 0, len(lines))
	var c compactor
	for _, ln := range lines {
		out = c.push(out, ln)
	}
	result := strings.Join(c.flush(out), "\n")
	return result, Stats{
		BytesIn: len(text), BytesOut: len(result),
		TokensEstIn: EstimateTokens(text), TokensEstOut: EstimateTokens(result),
	}
}

// compactor holds the prose line that may still repeat and whether the last emitted line
// was blank.
type compactor struct {
	pending string
	repeats int
	blank   bool
}

func (c *compactor) push(out []string, ln line) []string {
	if ln.kind == kindProse {
		text := squeeze(ln.text)
		if c.repeats > 0 && text == c.pending {
			c.repeats++
			return out
		}
		out = c.flush(out)
		c.pending, c.repeats, c.blank = text, 1, false
		return out
	}
	out = c.flush(out)
	if ln.kind == kindBlank {
		if c.blank {
			return out
		}
		c.blank = true
		return append(out, "")
	}
	c.blank = false
	return append(out, ln.text)
}

// flush emits the pending prose line, folded when it repeated.
func (c *compactor) flush(out []string) []string {
	if c.repeats == 0 {
		return out
	}
	text := c.pending
	if c.repeats > 1 {
		text = fmt.Sprintf("%s (x%d)", text, c.repeats)
	}
	c.repeats = 0
	return append(out, text)
}

// squeeze trims trailing blanks and collapses inner blank runs outside code spans. Leading
// indentation carries list nesting, so it stays.
func squeeze(text string) string {
	body := strings.TrimLeft(text, " \t")
	indent := text[:len(text)-len(body)]
	body = strings.TrimRight(body, " \t")
	var sb strings.Builder
	sb.WriteString(indent)
	last := 0
	for _, span := range inlineCodeRe.FindAllStringIndex(body, -1) {
		sb.WriteString(spaceRunRe.ReplaceAllString(body[last:span[0]], " "))
		sb.WriteString(body[span[0]:span[1]])
		last = span[1]
	}
	sb.WriteString(spaceRunRe.ReplaceAllString(body[last:], " "))
	return sb.String()
}
