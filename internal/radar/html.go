// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import "strings"

// maxTagBytes bounds one tag stripHTML removes (HISS-02); a longer run from '<' stays text.
const maxTagBytes = 1024

// htmlElements are the element names stripHTML removes as tags: every element of the element
// index of the WHATWG HTML standard (https://html.spec.whatwg.org/multipage/indices.html), the
// math and svg roots it embeds, and the obsolete presentational elements older feeds still carry.
// A name outside this table is not a tag, so "<n" in "k<n" stays text.
var htmlElements = map[string]bool{
	"a": true, "abbr": true, "address": true, "area": true, "article": true, "aside": true,
	"audio": true, "b": true, "base": true, "bdi": true, "bdo": true, "blockquote": true,
	"body": true, "br": true, "button": true, "canvas": true, "caption": true, "cite": true,
	"code": true, "col": true, "colgroup": true, "data": true, "datalist": true, "dd": true,
	"del": true, "details": true, "dfn": true, "dialog": true, "div": true, "dl": true, "dt": true,
	"em": true, "embed": true, "fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"form": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"head": true, "header": true, "hgroup": true, "hr": true, "html": true, "i": true, "iframe": true,
	"img": true, "input": true, "ins": true, "kbd": true, "label": true, "legend": true, "li": true,
	"link": true, "main": true, "map": true, "mark": true, "menu": true, "meta": true, "meter": true,
	"nav": true, "noscript": true, "object": true, "ol": true, "optgroup": true, "option": true,
	"output": true, "p": true, "picture": true, "pre": true, "progress": true, "q": true, "rp": true,
	"rt": true, "ruby": true, "s": true, "samp": true, "script": true, "search": true,
	"section": true, "select": true, "selectedcontent": true, "slot": true, "small": true,
	"source": true, "span": true, "strong": true, "style": true, "sub": true, "summary": true,
	"sup": true, "table": true, "tbody": true, "td": true, "template": true, "textarea": true,
	"tfoot": true, "th": true, "thead": true, "time": true, "title": true, "tr": true, "track": true,
	"u": true, "ul": true, "var": true, "video": true, "wbr": true,
	"math": true, "svg": true,
	"acronym": true, "big": true, "blink": true, "center": true, "font": true, "marquee": true,
	"nobr": true, "strike": true, "tt": true,
}

// stripHTML removes the well-formed tags of htmlElements (tagLength) and keeps every other
// character, so a '<' or '>' that opens no such tag, as in "n < 1000" or "k<n", stays text for
// escapeMarkdown to escape. A tag is removed without a trace, so "a<br>b" reads "ab".
func stripHTML(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '<' {
			if length := tagLength(text[i:]); length > 0 {
				i += length - 1
				continue
			}
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

// tagLength returns the length of the tag s starts with, or 0 when s does not start with a
// well-formed tag of an htmlElements element within maxTagBytes. An end tag is "</", the name,
// optional whitespace and '>'. A start tag is '<' and the name, then attributes each written
// name=value after whitespace, then an optional '/' and '>'. An attribute without a value is not
// accepted, so "a<b and m > 5" is a comparison, not a b tag.
func tagLength(s string) int {
	s = s[:min(len(s), maxTagBytes)]
	pos := 1
	closing := strings.HasPrefix(s[pos:], "/")
	if closing {
		pos++
	}
	nameEnd := pos + nameLength(s[pos:], false)
	if !htmlElements[strings.ToLower(s[pos:nameEnd])] {
		return 0
	}
	if closing {
		end := skipSpace(s, nameEnd)
		if strings.HasPrefix(s[end:], ">") {
			return end + 1
		}
		return 0
	}
	return startTagEnd(s, nameEnd)
}

// startTagEnd returns the length of the start tag whose name ends at pos, reading its attributes
// up to '>' or "/>", or 0 when what follows the name is not attribute syntax.
func startTagEnd(s string, pos int) int {
	for i := 0; i < maxTagBytes && pos < len(s); i++ {
		spaced := skipSpace(s, pos)
		switch {
		case strings.HasPrefix(s[spaced:], "/>"):
			return spaced + 2
		case strings.HasPrefix(s[spaced:], ">"):
			return spaced + 1
		case spaced == pos:
			return 0
		}
		pos = attributeEnd(s, spaced)
		if pos == 0 {
			return 0
		}
	}
	return 0
}

// attributeEnd returns the end of the name=value attribute starting at pos, or 0 when none
// starts there. The value is double-quoted, single-quoted, or a run without whitespace, quotes,
// '=', '<', '>' or '`'.
func attributeEnd(s string, pos int) int {
	nameEnd := pos + nameLength(s[pos:], true)
	if nameEnd == pos || !strings.HasPrefix(s[nameEnd:], "=") {
		return 0
	}
	value := s[nameEnd+1:]
	if quoted := strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'"); quoted {
		closing := strings.IndexByte(value[1:], value[0])
		if closing < 0 {
			return 0
		}
		return nameEnd + 1 + closing + 2
	}
	length := strings.IndexAny(value, " \t\n\r\f\"'=<>`")
	if length < 0 {
		length = len(value)
	}
	if length == 0 {
		return 0
	}
	return nameEnd + 1 + length
}

// nameLength returns the length of the ASCII name s starts with: a letter, then letters and
// digits, and for an attribute name also '-', '_', '.' and ':'. It is 0 when s starts with no
// letter.
func nameLength(s string, attribute bool) int {
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		digit := c >= '0' && c <= '9'
		punct := attribute && strings.IndexByte("-_.:", c) >= 0
		if !letter && (i == 0 || (!digit && !punct)) {
			return i
		}
	}
	return len(s)
}

// skipSpace returns the first position at or after pos that is not HTML whitespace.
func skipSpace(s string, pos int) int {
	for pos < len(s) && strings.IndexByte(" \t\n\r\f", s[pos]) >= 0 {
		pos++
	}
	return pos
}
