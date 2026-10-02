package caveman

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// ansiRe matches CSI sequences (colours, cursor moves), OSC sequences (titles,
	// hyperlinks) and the two-byte escapes.
	ansiRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
	// keptOrRunRe matches, at each position of a prose line outside its code spans, what keeps
	// its bytes there before it matches a run of blanks: an inline HTML tag, the target and
	// title of an inline link (parentheses nested once), a double-quoted string and a
	// single-quoted attribute value. Blanks inside an attribute value or a title are that
	// value, not layout.
	keptOrRunRe = regexp.MustCompile(`<[A-Za-z/!?][^<>]*>|\]\((?:[^()]|\([^()]*\))*\)|"[^"]*"|=[ \t]*'[^']*'|[ \t]{2,}`)
	// referenceDefinitionRe matches a link reference definition (CommonMark 0.31.2, 4.7). Its
	// title may be quoted either way or parenthesised, so the whole line keeps its inner blanks.
	referenceDefinitionRe = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:(?:[ \t]|$)`)
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
//   - in prose lines, trailing blanks go and inner runs of blanks collapse to one space,
//     outside code spans, inline HTML tags, link targets and titles and quoted values
//     (keptOrRunRe, referenceDefinitionRe), leading indentation kept;
//   - a run of blank lines becomes one blank line;
//   - identical consecutive prose lines fold into one line ending in " (xN)".
//
// Fenced code, indented code (markIndentedCode), caveman:off regions and structured lines
// (headings, tables, HTML comments, ledger rows, hook protocol lines, evidence pointers) keep
// their bytes apart from ANSI and CRLF. A prose line that ends in a hard line break keeps the
// break and is never folded (proseEnd). Prose is never rewritten: no word is dropped or
// replaced.
func Compress(text string) (string, Stats) {
	lines, _ := scan(StripANSI(text))
	markIndentedCode(lines)
	out := make([]string, 0, len(lines))
	var c compactor
	for i, ln := range lines {
		out = c.push(out, ln, i+1 < len(lines) && lines[i+1].kind != kindBlank)
	}
	result := strings.Join(c.flush(out), "\n")
	return result, Stats{
		BytesIn: len(text), BytesOut: len(result),
		TokensEstIn: EstimateTokens(text), TokensEstOut: EstimateTokens(result),
	}
}

// StripANSI removes the escape sequences Compress removes (CSI, OSC and the two-byte escapes)
// and nothing else. A caller that proves a compression with Floor compares against this text:
// the parameters of an escape hold digits, and Floor would read them as numbers of the text.
func StripANSI(text string) string {
	return ansiRe.ReplaceAllString(text, "")
}

// compactor holds the prose line that may still repeat and whether the last emitted line
// was blank.
type compactor struct {
	pending string
	repeats int
	blank   bool
}

// push appends ln to out; followed says a line that is not blank comes after it. A prose line
// ending in a hard line break is emitted as it is: folding it into its neighbour would merge two
// rendered lines.
func (c *compactor) push(out []string, ln line, followed bool) []string {
	if ln.kind == kindProse {
		text, held := squeeze(ln, followed)
		if held {
			c.blank = false
			return append(c.flush(out), text)
		}
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

// squeeze trims trailing blanks and collapses inner blank runs outside code spans, a span
// that wraps onto the line included. Leading indentation carries list nesting, so it stays,
// and so does what the line ending needs (proseEnd). A link reference definition keeps its
// inner blanks whole. It reports a line held out of folding.
func squeeze(ln line, followed bool) (string, bool) {
	text, end, held := proseEnd(ln, followed)
	if referenceDefinitionRe.MatchString(text) {
		return text + end, held
	}
	last := len(text) - len(strings.TrimLeft(text, " \t"))
	var sb strings.Builder
	sb.WriteString(text[:last])
	for _, piece := range ln.spans {
		lo, hi := min(max(piece.start, last), len(text)), min(piece.end, len(text))
		if hi <= lo {
			continue
		}
		sb.WriteString(collapseBlanks(text[last:lo]))
		sb.WriteString(text[lo:hi])
		last = hi
	}
	sb.WriteString(collapseBlanks(text[last:]))
	sb.WriteString(end)
	return sb.String(), held
}

// collapseBlanks turns every run of blanks in text into one space, except inside what
// keptOrRunRe keeps.
func collapseBlanks(text string) string {
	return keptOrRunRe.ReplaceAllStringFunc(text, func(match string) string {
		if match[0] == ' ' || match[0] == '\t' {
			return " "
		}
		return match
	})
}
