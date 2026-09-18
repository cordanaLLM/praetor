package caveman

import (
	"regexp"
	"strings"
	"unicode"
)

// lineKind classifies one line of Markdown-ish text. Only kindProse is linted as prose;
// every other kind is protected from the prose rules of Check and from Compress.
type lineKind int

const (
	kindProse lineKind = iota
	kindBlank
	kindCode
	kindOff
	kindStructured
)

const (
	// OffMarker opens a region that Check and Compress leave alone, for example a quoted
	// prose sample inside a skill. OnMarker closes it. Report.OffRegions counts the
	// regions, so an escape stays visible.
	OffMarker = "<!-- caveman:off -->"
	OnMarker  = "<!-- caveman:on -->"
)

var (
	inlineCodeRe = regexp.MustCompile("``[^`]*``|`[^`\n]*`")
	linkTargetRe = regexp.MustCompile(`\]\([^)\s]*\)`)
	urlRe        = regexp.MustCompile(`<?https?://[^\s>)]+>?`)
	headingRe    = regexp.MustCompile(`^#{1,6}(\s|$)`)
	// ledgerFieldRe matches engine-written ledger lines such as the STATE.md
	// "- **Tasks**: 3 open | **Open Bugs**: 0" row: bold fields split by pipes.
	ledgerFieldRe = regexp.MustCompile(`^[-*] \*\*[^*]+\*\*:.*\|\s*\*\*`)
	// protocolRe matches the hook protocol lines the agent hooks parse.
	protocolRe = regexp.MustCompile(`^PRAETOR_[A-Z0-9_]+(=.*)?$`)
	evidenceRe = regexp.MustCompile(`^(?:[-*] )?evidence: \S+ sha256:[0-9a-f]{12} lines:\d+`)
)

// line is one classified input line; num is 1-based. lang and edge describe fenced code:
// the info string of the fence and whether the line is a fence delimiter itself.
type line struct {
	num  int
	text string
	kind lineKind
	lang string
	edge bool
}

// scanner carries region state from one line to the next.
type scanner struct {
	fence      string
	lang       string
	off        bool
	comment    bool
	offRegions int
	offOpen    int
}

// scan splits text into classified lines. CRLF input classifies exactly like LF input, so
// a Windows checkout lints the same as a Linux one (HISS-21).
func scan(text string) ([]line, scanner) {
	raws := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := make([]line, 0, len(raws))
	var s scanner
	for i, raw := range raws {
		lines = append(lines, s.next(i+1, raw))
	}
	return lines, s
}

func (s *scanner) next(num int, raw string) line {
	trimmed := strings.TrimSpace(raw)
	ln := line{num: num, text: raw}
	switch {
	case s.off:
		s.off = trimmed != OnMarker
		ln.kind = kindOff
	case s.fence != "":
		ln.kind, ln.lang = kindCode, s.lang
		if strings.HasPrefix(trimmed, s.fence) && strings.Trim(trimmed, s.fence[:1]) == "" {
			s.fence, ln.edge = "", true
		}
	case s.comment:
		s.comment = !strings.Contains(trimmed, "-->")
		ln.kind = kindStructured
	default:
		ln.kind = s.open(num, trimmed)
		if ln.kind == kindCode {
			ln.lang, ln.edge = s.lang, true
		}
	}
	return ln
}

// open classifies a line outside every region and opens a region when the line starts one.
func (s *scanner) open(num int, trimmed string) lineKind {
	switch {
	case trimmed == "":
		return kindBlank
	case trimmed == OffMarker:
		s.off, s.offOpen = true, num
		s.offRegions++
		return kindOff
	case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
		run := len(trimmed) - len(strings.TrimLeft(trimmed, trimmed[:1]))
		s.fence, s.lang = trimmed[:run], strings.TrimSpace(trimmed[run:])
		return kindCode
	case strings.HasPrefix(trimmed, "<!--"):
		s.comment = !strings.Contains(trimmed, "-->")
		return kindStructured
	case isStructured(trimmed):
		return kindStructured
	}
	return kindProse
}

// isStructured reports lines whose shape a parser or a reader depends on: headings, table
// rows, ledger field rows, hook protocol lines and evidence pointers.
func isStructured(trimmed string) bool {
	return headingRe.MatchString(trimmed) || strings.HasPrefix(trimmed, "|") ||
		ledgerFieldRe.MatchString(trimmed) || protocolRe.MatchString(trimmed) ||
		evidenceRe.MatchString(trimmed)
}

// proseOf masks what is not prose inside a prose line: code spans, link targets and URLs.
func proseOf(text string) string {
	masked := inlineCodeRe.ReplaceAllString(text, " ")
	masked = linkTargetRe.ReplaceAllString(masked, "]")
	return urlRe.ReplaceAllString(masked, " ")
}

// proseWords returns the tokens of prose that carry at least one letter, lower-cased and
// trimmed of surrounding punctuation. List markers, numbers and symbols are not words.
func proseWords(prose string) []string {
	fields := strings.Fields(prose)
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		if word := strings.ToLower(strings.TrimFunc(field, notLetter)); word != "" {
			words = append(words, word)
		}
	}
	return words
}

func notLetter(r rune) bool {
	return !unicode.IsLetter(r)
}
