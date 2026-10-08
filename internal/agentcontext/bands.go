package agentcontext

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Band markers split AGENTS.md into the three cache bands. A marker is a line of its own
// outside a code fence; it opens its band and the band runs to the next marker. The markers
// are removed from the compiled files. A line before the first marker belongs to the head.
const (
	BandHeadMarker   = "<!-- praetor:head -->"
	BandConfigMarker = "<!-- praetor:config -->"
	BandTailMarker   = "<!-- praetor:tail -->"
)

// band numbers the cache bands in emit order. A client keeps the longest unchanged prefix of
// a file in its prompt cache, so the text that never changes comes first, the text a
// configuration edit changes second and the text the repository state changes last.
type band int

const (
	bandHead band = iota
	bandConfig
	bandTail
	bandCount
	// bandMarker flags a marker line, which no band emits.
	bandMarker band = -1
)

// RenderEnv carries the only sanctioned sources of time and order into a render. Code that
// needs the time calls Now, and the render visits its targets in the order Seed shuffles them
// to, so the stability check (compile-context --verify-stable) can vary both and require
// identical bytes. A nil env is the production render: the real clock and registry order.
type RenderEnv struct {
	Now  func() time.Time
	Seed int64
}

// order returns the indexes 0..n-1 in the order the render visits them: registry order for a
// nil env, a Seed-dependent permutation otherwise.
func (e *RenderEnv) order(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	if e == nil || n == 0 {
		return idx
	}
	// A rotation by Seed and a reversal for an odd Seed: deterministic, no random source.
	shift := int(((e.Seed % int64(n)) + int64(n)) % int64(n))
	rotated := slices.Concat(idx[shift:], idx[:shift])
	if e.Seed%2 != 0 {
		slices.Reverse(rotated)
	}
	return rotated
}

// markerBand returns the band a trimmed marker line opens.
func markerBand(trimmed string) (band, bool) {
	switch trimmed {
	case BandHeadMarker:
		return bandHead, true
	case BandConfigMarker:
		return bandConfig, true
	case BandTailMarker:
		return bandTail, true
	}
	return bandHead, false
}

// scanBands labels every line with its band (bandMarker for a marker line) and reports
// whether the source carries any marker. A source without markers is unlayered.
func scanBands(lines []string) ([]band, bool) {
	bands := make([]band, len(lines))
	current := bandHead
	marked := false
	var fence util.MarkdownFence
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !fence.Inside(trimmed) {
			if next, ok := markerBand(trimmed); ok {
				current, marked = next, true
				bands[i] = bandMarker
				continue
			}
		}
		bands[i] = current
	}
	return bands, marked
}

// splitBands collects the lines of every band for one vendor target. Lines of that vendor's
// section are client configuration and always join the config band; lines of any other
// vendor's section, and marker lines, are left out. An empty section selects no vendor, so
// the head it returns is the head every vendor file shares. This is the one place the band
// membership of a line is decided (HISS-19): layoutBands and HeadBand both read it.
func splitBands(lines, owners []string, bands []band, section string) [bandCount][]string {
	var parts [bandCount][]string
	for i, line := range lines {
		b := bands[i]
		if b == bandMarker || (owners[i] != "" && owners[i] != section) || blankAfterMarker(lines, bands, i) {
			continue
		}
		if owners[i] != "" {
			b = bandConfig
		}
		parts[b] = append(parts[b], line)
	}
	return parts
}

// layoutBands emits the head, the config band and the tail in that order for one vendor
// target, whatever order the source wrote them in. Blank lines at a band edge are trimmed and
// the bands are joined by one blank line, so an edit inside a band moves no byte of an
// earlier band. The output ends with a newline exactly when the source does, whichever bands
// are present.
func layoutBands(lines, owners []string, bands []band, section string) string {
	parts := splitBands(lines, owners, bands, section)
	out := make([]string, 0, bandCount)
	for _, part := range parts {
		if text := trimBlankEdges(part); text != "" {
			out = append(out, text)
		}
	}
	text := strings.Join(out, "\n\n")
	if text != "" && lines[len(lines)-1] == "" {
		text += "\n"
	}
	return text
}

// blankAfterMarker reports whether line i is the blank line that follows a marker. The marker
// is removed, and the blank line after it goes too, so the marker leaves no double gap.
func blankAfterMarker(lines []string, bands []band, i int) bool {
	return i > 0 && bands[i-1] == bandMarker && strings.TrimSpace(lines[i]) == ""
}

// trimBlankEdges joins lines without blank lines at either edge.
func trimBlankEdges(lines []string) string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// HeadBand returns the head band every vendor file shares (vendor sections never join it) and
// whether content carries band markers. An unmarked source has no head. An over-budget source
// is an error, not an unmarked source.
func HeadBand(content string) (string, bool, error) {
	if lf, _, err := util.NormalizeLineEndingsStrict(content); err == nil {
		content = lf
	}
	lines, owners, err := ownVendorLines(content)
	if err != nil {
		return "", false, fmt.Errorf("head band: %w", err)
	}
	bands, marked := scanBands(lines)
	if !marked {
		return "", false, nil
	}
	return trimBlankEdges(splitBands(lines, owners, bands, "")[bandHead]), true, nil
}

// VolatileToken is one piece of head text that changes between runs.
type VolatileToken struct {
	Line  int
	Kind  string
	Match string
}

// String names the token for a failure line.
func (v VolatileToken) String() string {
	return fmt.Sprintf("head line %d: %s %q", v.Line, v.Kind, v.Match)
}

// maxVolatileFindings bounds the findings one scan lists (HISS-02).
const maxVolatileFindings = 20

// pathBoundary precedes a path so a relative path (src/etc/x) or a URL never matches.
const pathBoundary = "(?:^|[\\s`(\"'=<])"

// pathTail is one path character: anything but whitespace and a closing quote.
const pathTail = "[^\\s`)\"'>]"

// volatilePatterns match exact shapes only; prose that merely resembles one (a retry count, a
// pass count, a directory named in a sentence, a static 40-hex pin) is not volatile.
//   - timestamp: an RFC 3339 date-time with a zone.
//   - digest: sha256: followed by the full 64 hex digits.
//   - absolute path: a user or volume root with one component below it (/home/name,
//     /Volumes/disk), a system root with two (/etc/ssl/certs; /var/tmp alone is prose), a
//     drive path (C:\x, C:/x) or a UNC path (\\host\share).
//   - run counter: run_id with a value, run #N, build number or build #N.
var volatilePatterns = [...]struct {
	kind string
	re   *regexp.Regexp
}{
	{"timestamp", regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)},
	{"digest", regexp.MustCompile(`(?i)\bsha256:[0-9a-f]{64}\b`)},
	{"absolute path", regexp.MustCompile(pathBoundary + "(?:" +
		"/(?:home|Users|Volumes)/" + pathTail + "+" +
		"|/(?:root|tmp|var|mnt|srv|opt|etc|usr|private|workspace|nix)(?:/[^\\s/`)\"'>]+){2,}" +
		"|[A-Za-z]:[\\\\/]" + pathTail + "+" +
		"|\\\\\\\\[^\\s\\\\]+\\\\" + pathTail + "+)")},
	{"run counter", regexp.MustCompile(`(?i)\brun[_-]?id\s*[:=]\s*\S*\d|\brun\s*#\s*\d+|\bbuild[ _-]?(?:number|no\.?)\s*[:=#]?\s*\d+|\bbuild\s*#\s*\d+`)},
}

// ScanVolatile returns the volatile tokens in a head band: RFC 3339 timestamps, sha256
// digests, absolute paths and run counters (volatilePatterns lists the exact shapes). The head is the prefix a client caches, so one such token
// breaks the cache at every run.
func ScanVolatile(head string) []VolatileToken {
	var found []VolatileToken
	for n, line := range strings.Split(head, "\n") {
		for _, p := range volatilePatterns {
			if m := p.re.FindString(line); m != "" {
				found = append(found, VolatileToken{Line: n + 1, Kind: p.kind, Match: strings.TrimSpace(m)})
			}
		}
		if len(found) >= maxVolatileFindings {
			return found[:maxVolatileFindings]
		}
	}
	return found
}

// InsertIntoConfigBand returns the LF text content with section placed in its config band, and
// whether content carries band markers at all (an unmarked source is returned unchanged). The
// section goes right after the config marker; a layered source with no config marker gets one,
// before the tail marker when there is a tail and at the end otherwise, so the section never
// lands in the head or the tail.
func InsertIntoConfigBand(content, section string) (string, bool) {
	lines := strings.Split(content, "\n")
	bands, marked := scanBands(lines)
	if !marked {
		return content, false
	}
	insert := append([]string{""}, strings.Split(strings.TrimRight(section, "\n"), "\n")...)
	insert = append(insert, "")
	at := markerLine(lines, bands, BandConfigMarker)
	var rest int
	if at >= 0 {
		at++
		rest = at
		for rest < len(lines) && strings.TrimSpace(lines[rest]) == "" {
			rest++
		}
	} else {
		insert = append([]string{BandConfigMarker}, insert...)
		at = markerLine(lines, bands, BandTailMarker)
		rest = at
		if at < 0 {
			// No tail: the section ends the file, after the last non-blank line.
			at, rest = endOfText(lines), len(lines)
			insert = append([]string{""}, insert...)
		}
	}
	out := slices.Concat(lines[:at], insert, lines[rest:])
	return strings.Join(out, "\n"), true
}

// markerLine returns the index of the first line that is the given marker, or -1.
func markerLine(lines []string, bands []band, marker string) int {
	for i, line := range lines {
		if bands[i] == bandMarker && strings.TrimSpace(line) == marker {
			return i
		}
	}
	return -1
}

// endOfText returns the index just past the last non-blank line.
func endOfText(lines []string) int {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return end
}
