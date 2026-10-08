// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const rssFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:media="http://search.yahoo.com/mrss/">
<channel><title>Example blog</title><link>https://example.org/</link>
<item><title>First &amp; <![CDATA[best]]></title><link>https://example.org/first</link>
<pubDate>Thu, 1 Oct 2026 09:30:00 +0000</pubDate><media:title>ignored</media:title></item>
<item><title>Dated by Dublin Core</title><link>https://example.org/dc</link><dc:date>2026-10-02</dc:date></item>
<item><title>No date</title><link>https://example.org/none</link></item>
</channel></rss>`

const atomFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>Example releases</title>
<entry><title type="html">v2.0 &lt;b&gt;bold&lt;/b&gt;</title>
<link rel="self" href="https://example.org/self"/><link href="https://example.org/v2"/>
<updated>2026-10-03T10:00:00Z</updated><published>2026-10-02T10:00:00+02:00</published></entry>
<entry><title>Only updated</title><link rel="alternate" href="https://example.org/u"/><updated>2026-10-04T00:00:00Z</updated></entry>
</feed>`

const rdfFeed = `<?xml version="1.0"?>
<rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#" xmlns="http://purl.org/rss/1.0/" xmlns:dc="http://purl.org/dc/elements/1.1/">
<channel><title>Papers</title></channel>
<item><title>A paper</title><link>https://example.org/abs/1</link><dc:date>2026-10-05T00:00:00Z</dc:date></item>
</rdf:RDF>`

// Positive: RSS 2.0, Atom and RSS 1.0 entries are read with their title, link and most specific
// date; an entry without a readable date is counted, not dropped silently.
func TestParseFeed_Positive_Formats(t *testing.T) {
	rss, err := ParseFeed([]byte(rssFeed))
	if err != nil {
		t.Fatalf("rss: %v", err)
	}
	if len(rss.Items) != 2 || rss.Undated != 1 {
		t.Fatalf("rss = %+v", rss)
	}
	first := rss.Items[0]
	if first.Title != "First & best" || first.Link != "https://example.org/first" ||
		!first.Published.Equal(time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)) || first.DateOnly {
		t.Errorf("rss first = %+v", first)
	}
	if second := rss.Items[1]; !second.DateOnly || !second.Published.Equal(time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("rss second = %+v", second)
	}
	atom, err := ParseFeed([]byte(atomFeed))
	if err != nil || len(atom.Items) != 2 {
		t.Fatalf("atom = %+v, %v", atom, err)
	}
	if v2 := atom.Items[0]; v2.Title != "v2.0 <b>bold</b>" || v2.Link != "https://example.org/v2" ||
		!v2.Published.Equal(time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("atom published entry = %+v", v2)
	}
	if updated := atom.Items[1]; updated.Link != "https://example.org/u" || updated.Published.Day() != 4 {
		t.Errorf("atom updated entry = %+v", updated)
	}
	rdf, err := ParseFeed([]byte(rdfFeed))
	if err != nil || len(rdf.Items) != 1 || rdf.Items[0].Title != "A paper" {
		t.Fatalf("rdf = %+v, %v", rdf, err)
	}
}

// feedRefusals are documents ParseFeed must refuse, each with the sentinel or text expected.
var feedRefusals = []struct {
	name, body string
	sentinel   error
	want       string
}{
	{"doctype", `<?xml version="1.0"?><!DOCTYPE rss [<!ENTITY x "boom">]><rss><channel><item><title>&x;</title></item></channel></rss>`, ErrFeedDoctype, ""},
	{"external doctype", `<!DOCTYPE rss SYSTEM "https://example.org/rss.dtd"><rss/>`, ErrFeedDoctype, ""},
	{"html page", `<html><body>not a feed</body></html>`, ErrNotFeed, ""},
	{"empty document", ``, ErrNotFeed, ""},
	{"comment only", `<!-- nothing -->`, ErrNotFeed, ""},
	{"unclosed element", `<rss><channel><item><title>x</title>`, nil, "radar feed"},
	{"mismatched tags", `<rss><channel></item></rss>`, nil, "radar feed"},
	{"undefined entity", `<rss><channel><item><title>&bogus;</title></item></channel></rss>`, nil, "radar feed"},
	{"non-UTF-8 encoding", `<?xml version="1.0" encoding="ISO-8859-1"?><rss/>`, nil, "radar feed"},
}

// Negative: a document type or entity declaration, a document that is not a feed, malformed XML,
// an undeclared entity, a foreign encoding and an oversized document are errors, never an empty
// feed.
func TestParseFeed_Negative_Refusals(t *testing.T) {
	for _, tc := range feedRefusals {
		_, err := ParseFeed([]byte(tc.body))
		if err == nil || (tc.sentinel != nil && !errors.Is(err, tc.sentinel)) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %v %q", tc.name, err, tc.sentinel, tc.want)
		}
	}
	oversize := make([]byte, MaxFeedBytes+1)
	if _, err := ParseFeed(oversize); !errors.Is(err, ErrFeedTooLarge) {
		t.Errorf("oversize: %v, want ErrFeedTooLarge", err)
	}
}

// nested renders a feed whose single entry's title sits inside depth elements in total.
func nested(depth int) string {
	// rss(1) > channel(2) > item(3) > title(4) > span... ; depth counts every element.
	extra := depth - 4
	return "<rss><channel><item><title>" + strings.Repeat("<s>", extra) + "deep" + strings.Repeat("</s>", extra) +
		"</title><pubDate>Thu, 1 Oct 2026 09:30:00 +0000</pubDate></item></channel></rss>"
}

// entries renders an RSS feed of count dated entries.
func entries(count int) string {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "<item><title>e%d</title><pubDate>Thu, 1 Oct 2026 09:30:00 +0000</pubDate></item>", i)
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

// Boundary: nesting of exactly MaxFeedDepth parses and one level more is refused; MaxFeedEntries
// entries parse and one more is refused; a document of exactly MaxFeedBytes is read; a feed with
// no entries is a valid empty feed; a long title is cut to the field cap.
func TestParseFeed_Boundary_Caps(t *testing.T) {
	if got, err := ParseFeed([]byte(nested(MaxFeedDepth))); err != nil || len(got.Items) != 1 || got.Items[0].Title != "deep" {
		t.Errorf("depth %d: %+v, %v", MaxFeedDepth, got, err)
	}
	if _, err := ParseFeed([]byte(nested(MaxFeedDepth + 1))); !errors.Is(err, ErrFeedTooDeep) {
		t.Errorf("depth %d: %v, want ErrFeedTooDeep", MaxFeedDepth+1, err)
	}
	if got, err := ParseFeed([]byte(entries(MaxFeedEntries))); err != nil || len(got.Items) != MaxFeedEntries {
		t.Errorf("%d entries: %d, %v", MaxFeedEntries, len(got.Items), err)
	}
	if _, err := ParseFeed([]byte(entries(MaxFeedEntries + 1))); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("%d entries: %v", MaxFeedEntries+1, err)
	}
	body := "<rss><channel></channel></rss>"
	exact := body + "<!--" + strings.Repeat("x", MaxFeedBytes-len(body)-7) + "-->"
	if got, err := ParseFeed([]byte(exact)); err != nil || len(exact) != MaxFeedBytes || len(got.Items) != 0 || got.Undated != 0 {
		t.Errorf("%d-byte empty feed: %+v, %v", len(exact), got, err)
	}
	long := "<rss><channel><item><title>" + strings.Repeat("t", 2*maxFieldBytes) +
		"</title><pubDate>Thu, 1 Oct 2026 09:30:00 +0000</pubDate></item></channel></rss>"
	if got, err := ParseFeed([]byte(long)); err != nil || len(got.Items[0].Title) != maxFieldBytes {
		t.Errorf("long title kept %d bytes, %v; want %d", len(got.Items[0].Title), err, maxFieldBytes)
	}
}

// hostZones are local zones the RFC 822 date tests run under. Each carries the abbreviation of a
// zone the tests parse, which is how time.Parse would let the host's zone decide an instant.
// Fixed zones need no tz database, so the tests run alike on Linux, macOS and Windows.
var hostZones = []*time.Location{
	time.UTC, time.FixedZone("EST", -5*3600), time.FixedZone("CEST", 2*3600), time.FixedZone("IST", 5*3600+1800),
}

// underHostZones runs check once with each of hostZones as the local zone. It replaces
// time.Local, so no test of this package may call t.Parallel.
func underHostZones(t *testing.T, check func(t *testing.T)) {
	t.Helper()
	saved := time.Local
	t.Cleanup(func() { time.Local = saved })
	for _, zone := range hostZones {
		time.Local = zone
		t.Run("local="+zone.String(), check)
	}
	time.Local = saved
}

// pubDateFeed renders an RSS feed with one entry per pubDate.
func pubDateFeed(dates ...string) []byte {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i, date := range dates {
		fmt.Fprintf(&b, "<item><title>e%d</title><pubDate>%s</pubDate></item>", i, date)
	}
	b.WriteString("</channel></rss>")
	return []byte(b.String())
}

// rfc822Dates are RFC 822 and RFC 1123 dates with the UTC instant each names: every named zone
// RFC 822 section 5 defines and UTC, matched without regard to case, numeric zones, and
// two-digit years.
var rfc822Dates = []struct {
	raw  string
	want time.Time
}{
	{"Thu, 01 Oct 2026 09:30:00 EST", time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 EDT", time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 CST", time.Date(2026, 10, 1, 15, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 CDT", time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 MST", time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 MDT", time.Date(2026, 10, 1, 15, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 PST", time.Date(2026, 10, 1, 17, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30 PDT", time.Date(2026, 10, 1, 16, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 GMT", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 UTC", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"01 Oct 2026 09:30 utc", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"01 Oct 2026 09:30:00 UT", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"01 Oct 2026 09:30 Z", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 est", time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 +0200", time.Date(2026, 10, 1, 7, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 2026 09:30:00 -0000", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
	{"Thu, 01 Oct 26 09:30:00 EST", time.Date(2026, 10, 1, 14, 30, 0, 0, time.UTC)},
	{"01 Oct 26 09:30 +0000", time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)},
}

// Positive: an RFC 822 date with a named or numeric zone, or a two-digit year, is the same
// instant whatever the host's local zone is.
func TestParseFeed_Positive_RFC822Zones(t *testing.T) {
	underHostZones(t, func(t *testing.T) {
		for _, tc := range rfc822Dates {
			got, err := ParseFeed(pubDateFeed(tc.raw))
			if err != nil || len(got.Items) != 1 || !got.Items[0].Published.Equal(tc.want) {
				t.Errorf("%q: %+v, %v; want %s", tc.raw, got, err, tc.want)
			}
		}
	})
}

// Negative: a zone name outside the table, a military letter other than Z, a date without a
// zone, a malformed numeric zone and every date form outside the accepted three (RFC 850,
// asctime, a colon offset in an RFC 822 date, a date-time without a zone or without the T) leave
// the entry undated on every host, instead of being placed by the host's local zone or at offset
// zero.
func TestParseFeed_Negative_UnresolvedZones(t *testing.T) {
	unresolved := []string{
		"Thu, 01 Oct 2026 09:30:00 CEST",
		"Thu, 01 Oct 2026 09:30:00 BST",
		"Thu, 01 Oct 2026 09:30:00 IST",
		"Thu, 01 Oct 2026 09:30:00 A",
		"Thu, 01 Oct 2026 09:30:00 Y",
		"Thu, 01 Oct 2026 09:30:00",
		"Thu, 01 Oct 2026 09:30:00 +02",
		"Thu, 01 Oct 2026 09:30:00 0200",
		"Thu, 01 Oct 2026 09:30:00 +00:00",
		"Thursday, 01-Oct-26 09:30:00 GMT",
		"Thu Oct  1 09:30:00 2026",
		"2026-10-01T09:30:00",
		"2026-10-01 09:30:00Z",
		"1 October 2026",
	}
	underHostZones(t, func(t *testing.T) {
		got, err := ParseFeed(pubDateFeed(unresolved...))
		if err != nil || len(got.Items) != 0 || got.Undated != len(unresolved) {
			t.Errorf("unresolved zones: %+v, %v; want %d undated", got, err, len(unresolved))
		}
	})
}

// Boundary: a two-digit year takes the RFC 5322 section 4.3 century, 49 the last year read as
// 20xx and 50 the first read as 19xx, on every host.
func TestParseFeed_Boundary_TwoDigitYear(t *testing.T) {
	years := map[string]int{"00": 2000, "49": 2049, "50": 1950, "68": 1968, "69": 1969, "99": 1999}
	underHostZones(t, func(t *testing.T) {
		for digits, want := range years {
			raw := "1 Jan " + digits + " 00:00 GMT"
			got, err := ParseFeed(pubDateFeed(raw))
			if err != nil || len(got.Items) != 1 || got.Items[0].Published.Year() != want {
				t.Errorf("%q: %+v, %v; want year %d", raw, got, err, want)
			}
		}
	})
}
