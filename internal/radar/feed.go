// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Feed bounds (HISS-02). A feed is untrusted input: its size, nesting, entry count and the text
// kept per field are capped, and every token consumes at least one input byte, so the token loop
// is bounded by the input length.
const (
	// MaxFeedBytes caps one feed document.
	MaxFeedBytes = 4 << 20
	// MaxFeedDepth caps element nesting.
	MaxFeedDepth = 32
	// MaxFeedEntries caps the entries one feed may carry; a longer feed fails rather than being
	// cut, so the digest never claims a partial feed is complete.
	MaxFeedEntries = 5000
	// maxFieldBytes caps the text kept for one entry field; the digest shows far less.
	maxFieldBytes = 8 << 10
)

var (
	// ErrFeedDoctype is a feed carrying a document type or entity declaration. Such
	// declarations are how entity expansion attacks start, and no feed needs one.
	ErrFeedDoctype = errors.New("document type declarations are refused")
	// ErrFeedTooLarge is a feed over MaxFeedBytes.
	ErrFeedTooLarge = errors.New("feed exceeds the byte cap")
	// ErrFeedTooDeep is a feed nesting elements deeper than MaxFeedDepth.
	ErrFeedTooDeep = errors.New("feed exceeds the nesting cap")
	// ErrNotFeed is an XML document whose root is not an RSS, RDF or Atom feed.
	ErrNotFeed = errors.New("document is not an RSS or Atom feed")
)

// Entry is one item a source published: a feed entry or a release.
type Entry struct {
	Title     string
	Link      string
	Published time.Time
	// DateOnly marks a Published value that carried a calendar date and no time; the window
	// places it by date (Window.ContainsDate).
	DateOnly bool
}

// Entries is what reading one source returned: its dated entries, and how many entries carried
// no date the reader could read, which no window can place and the digest therefore names.
type Entries struct {
	Items   []Entry
	Undated int
}

// feedDateFields are the entry fields that carry a date, most specific first: Atom published,
// RSS pubDate, Dublin Core date, then the modification dates.
var feedDateFields = [...]string{"published", "pubDate", "date", "issued", "updated", "modified"}

// feedDateLayouts are the date spellings feeds use: RFC 3339 (Atom, Dublin Core) and the RFC 822
// family (RSS), with and without a weekday, a numeric zone and seconds.
var feedDateLayouts = [...]string{
	time.RFC3339Nano,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 2006 15:04 MST",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
}

// feedEntry collects the fields of one entry while it is parsed.
type feedEntry struct {
	title string
	link  string
	dates [len(feedDateFields)]string
}

// feedParser is the state of one feed parse. It is a flat state machine over the token stream,
// so nesting costs no recursion (HISS-01).
type feedParser struct {
	rootSeen   bool
	depth      int
	entry      *feedEntry
	entryDepth int
	field      string
	text       strings.Builder
	out        Entries
}

// ParseFeed reads an RSS 2.0, RSS 1.0 (RDF) or Atom 1.0 document. A document type declaration,
// a document over MaxFeedBytes, nesting over MaxFeedDepth, more than MaxFeedEntries entries or
// malformed XML is an error, never an empty feed.
func ParseFeed(data []byte) (Entries, error) {
	if len(data) > MaxFeedBytes {
		return Entries{}, fmt.Errorf("radar feed: %w: %d bytes, cap %d", ErrFeedTooLarge, len(data), MaxFeedBytes)
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	// The HTML entity names are a fixed table; resolving them expands nothing a feed declares.
	decoder.Entity = xml.HTMLEntity
	parser := &feedParser{}
	for i := 0; i <= len(data); i++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return parser.finish()
		}
		if err != nil {
			return Entries{}, fmt.Errorf("radar feed: %w", err)
		}
		if err := parser.consume(token); err != nil {
			return Entries{}, fmt.Errorf("radar feed: %w", err)
		}
	}
	return Entries{}, errors.New("radar feed: token bound exceeded")
}

func (p *feedParser) consume(token xml.Token) error {
	switch t := token.(type) {
	case xml.StartElement:
		return p.start(t)
	case xml.EndElement:
		p.end()
	case xml.CharData:
		p.chars(t)
	case xml.Directive:
		return ErrFeedDoctype
	}
	return nil
}

func (p *feedParser) start(element xml.StartElement) error {
	p.depth++
	if p.depth > MaxFeedDepth {
		return fmt.Errorf("%w of %d", ErrFeedTooDeep, MaxFeedDepth)
	}
	local := element.Name.Local
	if p.depth == 1 {
		p.rootSeen = true
		return feedRoot(local)
	}
	if p.entry == nil {
		return p.openEntry(local)
	}
	if p.depth == p.entryDepth+1 {
		p.field = local
		p.text.Reset()
		if local == "link" {
			p.entry.linkAttribute(element.Attr)
		}
	}
	return nil
}

// feedRoot accepts the root elements of RSS 2.0 (rss), RSS 1.0 (RDF) and Atom (feed).
func feedRoot(local string) error {
	if local == "rss" || local == "RDF" || local == "feed" {
		return nil
	}
	return fmt.Errorf("%w: root element %q", ErrNotFeed, local)
}

// openEntry starts an entry at an RSS item or an Atom entry element.
func (p *feedParser) openEntry(local string) error {
	if local != "item" && local != "entry" {
		return nil
	}
	if len(p.out.Items)+p.out.Undated >= MaxFeedEntries {
		return fmt.Errorf("feed carries more than %d entries", MaxFeedEntries)
	}
	p.entry = &feedEntry{}
	p.entryDepth = p.depth
	return nil
}

func (p *feedParser) end() {
	if p.entry != nil {
		switch p.depth {
		case p.entryDepth + 1:
			p.entry.set(p.field, p.text.String())
			p.field = ""
		case p.entryDepth:
			p.closeEntry()
		}
	}
	p.depth--
}

func (p *feedParser) chars(text xml.CharData) {
	if p.entry == nil || p.field == "" {
		return
	}
	room := maxFieldBytes - p.text.Len()
	if room <= 0 {
		return
	}
	p.text.Write(text[:min(len(text), room)])
}

// closeEntry files the finished entry as dated or undated.
func (p *feedParser) closeEntry() {
	published, dateOnly, ok := p.entry.date()
	if ok {
		p.out.Items = append(p.out.Items, Entry{Title: p.entry.title, Link: p.entry.link, Published: published, DateOnly: dateOnly})
	} else {
		p.out.Undated++
	}
	p.entry = nil
}

// finish returns the entries once the document ended; a document without a root element is no
// feed. The strict decoder reports an unclosed element before the end, so the root has closed.
func (p *feedParser) finish() (Entries, error) {
	if !p.rootSeen {
		return Entries{}, fmt.Errorf("radar feed: %w: no root element", ErrNotFeed)
	}
	return p.out, nil
}

// set records one field's text; the first non-empty value of a field wins, so an extension
// element of the same local name later in the entry cannot replace it.
func (e *feedEntry) set(field, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	switch field {
	case "title":
		if e.title == "" {
			e.title = text
		}
	case "link":
		if e.link == "" {
			e.link = text
		}
	default:
		for i := 0; i < len(feedDateFields); i++ {
			if feedDateFields[i] == field && e.dates[i] == "" {
				e.dates[i] = text
			}
		}
	}
}

// linkAttribute takes an Atom link's href when the link is the entry's alternate (rel absent or
// "alternate"); RSS links carry the address as text instead.
func (e *feedEntry) linkAttribute(attributes []xml.Attr) {
	href, rel := "", ""
	for i := 0; i < len(attributes) && i < MaxFeedDepth; i++ {
		switch attributes[i].Name.Local {
		case "href":
			href = strings.TrimSpace(attributes[i].Value)
		case "rel":
			rel = attributes[i].Value
		}
	}
	if e.link == "" && href != "" && (rel == "" || rel == "alternate") {
		e.link = href
	}
}

// date returns the entry's most specific readable date.
func (e *feedEntry) date() (time.Time, bool, bool) {
	for i := 0; i < len(e.dates); i++ {
		if e.dates[i] == "" {
			continue
		}
		if published, dateOnly, ok := parseFeedDate(e.dates[i]); ok {
			return published, dateOnly, true
		}
	}
	return time.Time{}, false, false
}

// parseFeedDate reads one date in a feedDateLayouts spelling, or a calendar date, as UTC.
func parseFeedDate(raw string) (time.Time, bool, bool) {
	if date, err := time.Parse(dateLayout, raw); err == nil {
		return date.UTC(), true, true
	}
	for i := 0; i < len(feedDateLayouts); i++ {
		if instant, err := time.Parse(feedDateLayouts[i], raw); err == nil {
			return instant.UTC(), false, true
		}
	}
	return time.Time{}, false, false
}
