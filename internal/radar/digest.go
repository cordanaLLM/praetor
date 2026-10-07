// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// Digest rendering bounds (HISS-02).
const (
	// MaxSectionItems caps the items one section lists; the rest are counted, not listed.
	MaxSectionItems = 50
	// MaxTitleRunes caps a rendered item title.
	MaxTitleRunes = 200
	// MaxReasonRunes caps a rendered failure reason.
	MaxReasonRunes = 300
	// maxNeutralizeBytes caps the text Neutralize reads; nothing it renders comes near it.
	maxNeutralizeBytes = 64 << 10
)

// markdownEscapes are the ASCII punctuation characters Neutralize escapes with a backslash, so
// untrusted text cannot open emphasis, code, a link, an image, an entity or an HTML tag. An
// escaped '<' is literal text in CommonMark, never the start of raw HTML or an autolink.
const markdownEscapes = "\\`*_[]()!~&<>"

// linkUnsafe are the characters that keep a link out of a Markdown link destination; a link
// carrying one is not rendered as a link.
const linkUnsafe = "<>()[]\"'`\\|{}"

// referenceSegments are the path segments under which a forge serves an issue, a pull request or
// a discussion; a link to one is printed as code, not linked, so posting the digest does not
// cross-reference it.
var referenceSegments = map[string]bool{"issues": true, "pull": true, "pulls": true, "discussions": true, "merge_requests": true}

// DigestTitle is the title of the digest of window w: the dates its half-open interval spans.
func DigestTitle(w Window) string {
	return "Radar digest " + w.Since.Format(dateLayout) + " to " + w.Now.Format(dateLayout)
}

// Render writes the digest as Markdown. A digest with no section and no failure renders as no
// bytes at all, so an unchanged week writes an empty file; Collect gives a source a section only
// for an entry inside the window, so undated entries alone never make a digest non-empty. Every
// text a source supplied passes through Neutralize; the source ids and URLs come from the
// validated registry.
func Render(d Digest) []byte {
	if len(d.Sections) == 0 && len(d.Failed) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("# " + DigestTitle(d.Window) + "\n\n")
	fmt.Fprintf(&b, "Window: %s <= t < %s (UTC). Sources read: %d, failed: %d.\n",
		d.Window.Since.Format(time.RFC3339), d.Window.Now.Format(time.RFC3339), d.Read, len(d.Failed))
	for i := 0; i < len(d.Sections) && i < MaxSources; i++ {
		renderSection(&b, d.Sections[i])
	}
	if len(d.Failed) > 0 {
		b.WriteString("\n## Failed sources\n\n")
		for i := 0; i < len(d.Failed) && i < MaxSources; i++ {
			fmt.Fprintf(&b, "- %s: %s\n", Neutralize(d.Failed[i].Source.ID, MaxIDBytes),
				Neutralize(d.Failed[i].Reason, MaxReasonRunes))
		}
	}
	return []byte(b.String())
}

func renderSection(b *strings.Builder, s Section) {
	fmt.Fprintf(b, "\n## %s (%s)\n\nSource: <%s>\n\n", Neutralize(s.Source.ID, MaxIDBytes), s.Source.Kind, s.Source.URL)
	shown := min(len(s.Items), MaxSectionItems)
	for i := 0; i < shown; i++ {
		b.WriteString("- " + renderItem(s.Items[i]) + "\n")
	}
	if hidden := len(s.Items) - shown; hidden > 0 {
		fmt.Fprintf(b, "- %d more not shown (section cap %d).\n", hidden, MaxSectionItems)
	}
	switch {
	case s.Undated == 1:
		b.WriteString("\n1 entry of this source carries no readable date and is in no window.\n")
	case s.Undated > 1:
		fmt.Fprintf(b, "\n%d entries of this source carry no readable date and are in no window.\n", s.Undated)
	}
}

// renderItem renders one entry: its date, its neutralised title, and its link when the link is
// safe to render, as code when it addresses an issue, a pull request or a discussion.
func renderItem(entry Entry) string {
	title := Neutralize(entry.Title, MaxTitleRunes)
	if title == "" {
		title = "(untitled)"
	}
	line := entry.Published.UTC().Format(dateLayout) + " "
	link, ok := safeLink(entry.Link)
	switch {
	case !ok:
		return line + title
	case referenceLink(link):
		return line + title + " (`" + link + "`)"
	}
	return line + "[" + title + "](" + link + ")"
}

// safeLink returns raw when it is an absolute http or https URL of printable ASCII that cannot
// leave a Markdown link destination.
func safeLink(raw string) (string, bool) {
	link := strings.TrimSpace(raw)
	if link == "" || len(link) > MaxURLBytes || strings.ContainsAny(link, linkUnsafe) || strings.IndexFunc(link, notPrintableASCII) >= 0 {
		return "", false
	}
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	return link, true
}

// notPrintableASCII reports whether r is outside printable ASCII without the space.
func notPrintableASCII(r rune) bool {
	return r < 0x21 || r > 0x7e
}

// referenceLink reports whether link addresses an issue, a pull request or a discussion.
func referenceLink(link string) bool {
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	segments := pathSegments(parsed.Path)
	for i := 0; i+1 < len(segments) && i < maxPathSegments; i++ {
		if referenceSegments[segments[i]] {
			return true
		}
	}
	return false
}

// Neutralize turns untrusted text into one line of inert Markdown of at most maxRunes runes
// (plus an ellipsis when cut). It decodes HTML entities and removes the well-formed tags of HTML
// elements (stripHTML), keeping every other '<' and '>' as text; drops control and invisible
// format characters; collapses line breaks and whitespace; removes '@', so no account is
// mentioned; breaks issue references (#12, GH-12) so none links; replaces '|' so no table breaks;
// defangs URLs so none links; and escapes the Markdown punctuation that opens emphasis, code,
// links, images, entities or HTML.
func Neutralize(text string, maxRunes int) string {
	if len(text) > maxNeutralizeBytes {
		text = text[:maxNeutralizeBytes]
	}
	text = stripHTML(html.UnescapeString(strings.ToValidUTF8(text, "")))
	text = breakReferences(strings.Map(inertRune, text))
	text = strings.NewReplacer("://", "[:]//", "www.", "www[.]").Replace(text)
	text = truncateRunes(strings.Join(strings.Fields(text), " "), maxRunes)
	return escapeMarkdown(text)
}

// inertRune maps whitespace controls to a space, drops other control and format characters and
// '@', and replaces '|' with a broken bar.
func inertRune(r rune) rune {
	switch {
	case r == '\n' || r == '\r' || r == '\t':
		return ' '
	case r == '@' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
		return -1
	case r == '|':
		return '¦'
	}
	return r
}

// breakReferences puts a space between '#' and a following digit, and turns "GH-" before a digit
// into "GH ", so the forge links no issue.
func breakReferences(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		next := i+1 < len(runes) && unicode.IsDigit(runes[i+1])
		switch {
		case runes[i] == '#' && next:
			b.WriteString("# ")
		case runes[i] == '-' && next && i >= 2 && strings.EqualFold(string(runes[i-2:i]), "gh"):
			b.WriteRune(' ')
		default:
			b.WriteRune(runes[i])
		}
	}
	return b.String()
}

// truncateRunes cuts text to maxRunes runes and marks the cut with an ellipsis.
func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if maxRunes < 1 || len(runes) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

// escapeMarkdown backslash-escapes the characters of markdownEscapes.
func escapeMarkdown(text string) string {
	var b strings.Builder
	for _, r := range text {
		if strings.ContainsRune(markdownEscapes, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
