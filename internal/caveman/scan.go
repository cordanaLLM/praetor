package caveman

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
)

// lineKind classifies one line of Markdown-ish text. kindProse is linted directly; table
// cells are extracted from kindStructured lines. Other kinds stay protected from prose
// rules. Compress preserves every structured line.
type lineKind int

const (
	kindProse lineKind = iota
	kindBlank
	kindCode
	kindOff
	kindStructured
	// kindFrontMatter is a leading YAML front matter block, delimiters included. Its shape
	// is a contract with the harness that loads the file, such as a skill's description,
	// not prose its author chose, so Check leaves it out of the prose rules the way it
	// leaves a table delimiter row out (#374). CheckRuntime still reads it.
	kindFrontMatter
)

const (
	// OffMarker opens a region that Check and Compress leave alone, for example a quoted
	// prose sample inside a skill. OnMarker closes it. Report.OffRegions counts the
	// regions, so an escape stays visible.
	OffMarker = "<!-- caveman:off -->"
	OnMarker  = "<!-- caveman:on -->"
)

var (
	linkTargetRe = regexp.MustCompile(`\]\([^)\s]*\)`)
	urlRe        = regexp.MustCompile(`(?i)<?https?://[^\s>]+>?`)
	headingRe    = regexp.MustCompile(`^#{1,6}(\s|$)`)
	// ledgerFieldRe matches engine-written ledger lines such as the STATE.md
	// "- **Tasks**: 3 open | **Open Bugs**: 0" row: bold fields split by pipes.
	ledgerFieldRe = regexp.MustCompile(`^[-*] \*\*[^*]+\*\*:.*\|\s*\*\*`)
	// protocolRe matches the hook protocol lines the agent hooks parse.
	protocolRe = regexp.MustCompile(`^PRAETOR_[A-Z0-9_]+(=.*)?$`)
	evidenceRe = regexp.MustCompile(`^(?:[-*+] )?evidence: \S+ sha256:[0-9a-f]{12} lines:\d+$`)
	// softBreakRe matches where a wrapped code span crossed a line: the line ending with the
	// blanks around it and the blockquote markers of the line the span continues on.
	softBreakRe = regexp.MustCompile(`[ \t]*\n[ \t]*(?:>[ \t]?)*`)
)

// line is one classified input line; num is 1-based. lang and edge describe fenced code:
// the info string of the fence and whether the line is a fence delimiter itself. shell says
// how the fence reads as commands (util.MarkdownShellFence) and code is a fenced line
// without the blockquote markers its fence sits in. spans are the inline code span pieces
// on a line outside fenced code, in order.
type line struct {
	num   int
	text  string
	kind  lineKind
	lang  string
	edge  bool
	shell util.MarkdownShell
	code  string
	spans []spanPiece
}

// spanPiece is the part of one inline code span that lies on one line: the byte range
// [start, end) of line.text, delimiters included. cont marks a piece that continues a span
// opened on an earlier line of the same paragraph.
type spanPiece struct {
	start, end int
	cont       bool
}

// lineItem is one fact text with the line it starts on.
type lineItem struct {
	num  int
	text string
}

// scanner carries region state from one line to the next. Fenced code is tracked by
// util.MarkdownFence, the repository's one fence-tracking implementation (HISS-19); lang
// holds the info string of the open fence, shell how it reads as commands, fenceOpen the
// line that opened it, for rule C13, and fenceQuote the blockquote depth it sits in, none of
// which the tracker carries. frontMatter counts the lines of a leading front matter block.
type scanner struct {
	fence       util.MarkdownFence
	fenceOpen   int
	fenceQuote  int
	lang        string
	shell       util.MarkdownShell
	off         bool
	comment     bool
	offRegions  int
	offOpen     int
	frontMatter int
}

// scan splits text into classified lines and records the inline code spans of each line
// outside fenced code. CRLF input classifies exactly like LF input, so a Windows checkout
// lints the same as a Linux one (HISS-21).
func scan(text string) ([]line, scanner) {
	raws := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]line, 0, len(raws))
	s := scanner{frontMatter: frontMatterLines(raws)}
	for i, raw := range raws {
		if i < s.frontMatter {
			lines = append(lines, line{num: i + 1, text: raw, kind: kindFrontMatter})
			continue
		}
		lines = append(lines, s.next(i+1, raw))
	}
	markCodeSpans(lines)
	return lines, s
}

// frontMatterLines returns how many lines a leading YAML front matter block takes, 0 when
// the text opens with none. util.MarkdownFrontMatterEnd finds the delimiters; the block
// between them must also decode as a YAML mapping, so a thematic break above a paragraph
// and a second break never passes for front matter and hides that paragraph from the lint.
func frontMatterLines(raws []string) int {
	end := util.MarkdownFrontMatterEnd(raws)
	if end == 0 {
		return 0
	}
	var mapping map[string]any
	block := strings.Join(raws[1:end-1], "\n")
	if err := util.DecodeYAMLDocument([]byte(block), &mapping, util.YAMLDocumentOptions{AllowEmpty: true}); err != nil {
		return 0
	}
	return end
}

func (s *scanner) next(num int, raw string) line {
	if s.fence.Open() {
		if depth, body := unquote(raw, s.fenceQuote); depth == s.fenceQuote {
			return s.fenced(num, raw, body)
		}
		// The blockquote holding the fence ended, and CommonMark closes the fence with it.
		s.fence = util.MarkdownFence{}
	}
	trimmed := strings.TrimSpace(raw)
	ln := line{num: num, text: raw}
	switch {
	case s.off:
		s.off = trimmed != OnMarker
		ln.kind = kindOff
	case s.comment:
		s.comment = !strings.Contains(trimmed, "-->")
		ln.kind = kindStructured
	default:
		ln.kind = s.open(num, raw, trimmed)
		if ln.kind == kindCode {
			_, ln.code = unquote(raw, s.fenceQuote)
			ln.lang, ln.shell, ln.edge = s.lang, s.shell, true
		}
	}
	return ln
}

// fenced classifies a line inside an open fence; body is the line without the blockquote
// markers the fence sits in.
func (s *scanner) fenced(num int, raw, body string) line {
	closing := s.fence.Inside(strings.TrimSpace(body)) && !s.fence.Open()
	return line{num: num, text: raw, kind: kindCode, lang: s.lang, shell: s.shell, edge: closing, code: body}
}

// open classifies a line outside every region and opens a region when the line starts one.
// A fence opens inside a blockquote too: "> ```mermaid" starts fenced code, not prose that
// holds an empty code span (#356).
func (s *scanner) open(num int, raw, trimmed string) lineKind {
	depth, body := unquote(raw, len(raw))
	fenceLine := strings.TrimSpace(body)
	switch {
	case trimmed == "":
		return kindBlank
	case trimmed == OffMarker:
		s.off, s.offOpen = true, num
		s.offRegions++
		return kindOff
	case s.fence.Inside(fenceLine):
		marker := s.fence.Marker()
		s.lang = strings.TrimSpace(strings.TrimPrefix(fenceLine, marker))
		s.shell = util.MarkdownShellFence(util.MarkdownFenceLanguage(fenceLine, marker))
		s.fenceOpen, s.fenceQuote = num, depth
		return kindCode
	case strings.HasPrefix(trimmed, "<!--"):
		s.comment = !strings.Contains(trimmed, "-->")
		return kindStructured
	case isStructured(trimmed):
		return kindStructured
	}
	return kindProse
}

// unquote strips up to limit blockquote markers from the start of raw, each a '>' after
// optional indentation with one optional space after it, and returns how many it stripped
// and the rest of the line.
func unquote(raw string, limit int) (int, string) {
	rest := raw
	depth := 0
	for depth < limit {
		trimmed := strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		rest = strings.TrimPrefix(trimmed[1:], " ")
		depth++
	}
	return depth, rest
}

// isStructured reports lines whose shape a parser or a reader depends on: headings, table
// rows, ledger field rows, hook protocol lines and evidence pointers.
func isStructured(trimmed string) bool {
	return headingRe.MatchString(trimmed) || strings.HasPrefix(trimmed, "|") ||
		ledgerFieldRe.MatchString(trimmed) || protocolRe.MatchString(trimmed) ||
		evidenceRe.MatchString(trimmed)
}

// markCodeSpans records the inline code spans of every line outside fenced code, read by
// util.MarkdownCodeSpans. The prose lines of one paragraph are read as one text, because
// CommonMark lets a code span run across a soft line break (#320): a backtick left open at
// the end of a line pairs with the next line's first backtick instead of with the next
// backtick on its own line. A blank or non-prose line ends a paragraph and a list marker
// starts a new one, as paragraphs does; every other line is read alone.
func markCodeSpans(lines []line) {
	for start := 0; start < len(lines); {
		end := start + 1
		if lines[start].kind == kindProse {
			end = paragraphEnd(lines, start)
		}
		if lines[start].kind != kindCode {
			markParagraphSpans(lines[start:end])
		}
		start = end
	}
}

// paragraphEnd returns the index after the last prose line of the paragraph opened at start.
func paragraphEnd(lines []line, start int) int {
	end := start + 1
	for end < len(lines) && lines[end].kind == kindProse && !listItemRe.MatchString(strings.TrimSpace(lines[end].text)) {
		end++
	}
	return end
}

// markParagraphSpans reads lines as one text joined by line endings and gives each line the
// piece of every code span that covers it.
func markParagraphSpans(lines []line) {
	offsets := make([]int, len(lines))
	var joined strings.Builder
	for i, ln := range lines {
		if i > 0 {
			joined.WriteByte('\n')
		}
		offsets[i] = joined.Len()
		joined.WriteString(ln.text)
	}
	text := joined.String()
	first := 0
	for _, span := range util.MarkdownCodeSpans(text, len(text)) {
		for first+1 < len(lines) && offsets[first+1] <= span.Start {
			first++
		}
		for i := first; i < len(lines) && offsets[i] < span.End; i++ {
			lo := max(span.Start, offsets[i]) - offsets[i]
			hi := min(span.End, offsets[i]+len(lines[i].text)) - offsets[i]
			lines[i].spans = append(lines[i].spans, spanPiece{start: lo, end: hi, cont: span.Start < offsets[i]})
		}
	}
}

// codeSpanTexts returns every inline code span outside fenced code with the line it opens
// on: delimiters kept, and a span that wraps joined with one space where its line broke, so
// the wrapped and the unwrapped form of one span are the same fact. A span of only blanks
// carries no fact and is left out.
func codeSpanTexts(lines []line) []lineItem {
	var items []lineItem
	for _, ln := range lines {
		for _, piece := range ln.spans {
			text := ln.text[piece.start:piece.end]
			if piece.cont && len(items) > 0 {
				items[len(items)-1].text += "\n" + text
				continue
			}
			items = append(items, lineItem{num: ln.num, text: text})
		}
	}
	out := items[:0]
	for _, item := range items {
		item.text = softBreakRe.ReplaceAllString(item.text, " ")
		if strings.TrimSpace(strings.Trim(item.text, "`")) != "" {
			out = append(out, item)
		}
	}
	return out
}

// mapSpans returns ln.text with each code span piece replaced by replace(piece).
func (ln line) mapSpans(replace func(string) string) string {
	if len(ln.spans) == 0 {
		return ln.text
	}
	var sb strings.Builder
	last := 0
	for _, piece := range ln.spans {
		sb.WriteString(ln.text[last:piece.start])
		sb.WriteString(replace(ln.text[piece.start:piece.end]))
		last = piece.end
	}
	sb.WriteString(ln.text[last:])
	return sb.String()
}

// blankSpan masks a code span piece as one space.
func blankSpan(string) string {
	return " "
}

// proseOf masks what is not prose inside a line: code spans, link targets and URLs.
func proseOf(ln line) string {
	masked := ln.mapSpans(blankSpan)
	masked = linkTargetRe.ReplaceAllString(masked, "]")
	return urlRe.ReplaceAllString(masked, " ")
}

// proseSegments returns prose carried by a line. Table syntax stays structured for
// Compress, but each Markdown cell is linted independently by Check.
func proseSegments(ln line) []string {
	if ln.kind == kindProse {
		return []string{proseOf(ln)}
	}
	if ln.kind != kindStructured || !strings.HasPrefix(strings.TrimSpace(ln.text), "|") {
		return nil
	}
	masked := proseOf(ln)
	parts := strings.Split(strings.Trim(masked, "|"), "|")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if cell := strings.TrimSpace(part); cell != "" && !tableDelimiter(cell) {
			segments = append(segments, cell)
		}
	}
	return segments
}

func tableDelimiter(cell string) bool {
	trimmed := strings.Trim(cell, ":- ")
	return trimmed == "" && strings.Contains(cell, "-")
}

// proseWords returns the tokens of prose that carry at least one letter, lower-cased and
// trimmed of surrounding punctuation. List markers, numbers and symbols are not words.
func proseWords(prose string) []string {
	fields := strings.Fields(prose)
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		field = compatibilityFold(field)
		if word := strings.ToLower(strings.TrimFunc(field, notLetter)); word != "" {
			words = append(words, word)
		}
	}
	return words
}

func notLetter(r rune) bool {
	return !unicode.IsLetter(r)
}
