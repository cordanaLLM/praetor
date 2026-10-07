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

// feedDateLayouts are the RFC 822 family of date spellings RSS uses (RFC 822 section 5, with the
// four-digit year of RFC 1123 section 5.2.14), with and without a weekday and seconds. Every
// layout ends in a numeric zone, which fixes the instant whatever the host's local zone is;
// parseRFC822Date rewrites a named zone to its offset first. A layout whose year has two digits
// is marked, because RFC 5322 section 4.3 reads such years with a different century pivot than
// time.Parse does.
var feedDateLayouts = [...]struct {
	layout       string
	twoDigitYear bool
}{
	{"Mon, 2 Jan 2006 15:04:05 -0700", false},
	{"Mon, 2 Jan 2006 15:04 -0700", false},
	{"2 Jan 2006 15:04:05 -0700", false},
	{"2 Jan 2006 15:04 -0700", false},
	{"Mon, 2 Jan 06 15:04:05 -0700", true},
	{"Mon, 2 Jan 06 15:04 -0700", true},
	{"2 Jan 06 15:04:05 -0700", true},
	{"2 Jan 06 15:04 -0700", true},
}

// rfc822Zones are the zone names RFC 822 section 5 defines with a fixed meaning, as the offsets
// RFC 5322 section 4.3 gives them. The names are matched without regard to case.
//
// Any other name is left out on purpose and leaves the entry undated. time.Parse would resolve
// a name through the host's local zone, or give a name it does not know offset zero, so the same
// feed would place an entry differently on two hosts. RFC 5322 section 4.3 calls the military
// letters other than Z unpredictable in meaning, and says a zone of unknown meaning, such as
// CEST, carries no zone information; placing either in a window would rest on a guess.
var rfc822Zones = map[string]string{
	"UT": "+0000", "GMT": "+0000", "Z": "+0000",
	"EST": "-0500", "EDT": "-0400", "CST": "-0600", "CDT": "-0500",
	"MST": "-0700", "MDT": "-0600", "PST": "-0800", "PDT": "-0700",
}

// rfc5322CenturyPivot is the first year a two-digit year must not reach: RFC 5322 section 4.3
// reads 00 to 49 as 2000 to 2049 and 50 to 99 as 1950 to 1999, where time.Parse reads 50 to 68
// as 2050 to 2068.
const rfc5322CenturyPivot = 2050

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

// parseFeedDate reads one date as UTC: a calendar date, an RFC 3339 timestamp, or an RFC 822
// date whose zone is numeric or named in rfc822Zones. Every other spelling, a named zone outside
// rfc822Zones among them, reports false, so the entry is counted as undated.
func parseFeedDate(raw string) (time.Time, bool, bool) {
	if date, err := time.Parse(dateLayout, raw); err == nil {
		return date.UTC(), true, true
	}
	if instant, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return instant.UTC(), false, true
	}
	instant, ok := parseRFC822Date(raw)
	return instant, false, ok
}

// parseRFC822Date reads raw in a feedDateLayouts spelling after rewriting a named zone in its
// last field to the offset rfc822Zones gives it. A name outside rfc822Zones stays a name, which
// no layout accepts, because every layout reads a numeric zone.
func parseRFC822Date(raw string) (time.Time, bool) {
	if cut := strings.LastIndexByte(raw, ' '); cut >= 0 {
		if offset, named := rfc822Zones[strings.ToUpper(raw[cut+1:])]; named {
			raw = raw[:cut+1] + offset
		}
	}
	for i := 0; i < len(feedDateLayouts); i++ {
		instant, err := time.Parse(feedDateLayouts[i].layout, raw)
		if err != nil {
			continue
		}
		if feedDateLayouts[i].twoDigitYear && instant.Year() >= rfc5322CenturyPivot {
			instant = instant.AddDate(-100, 0, 0)
		}
		return instant.UTC(), true
	}
	return time.Time{}, false
}
