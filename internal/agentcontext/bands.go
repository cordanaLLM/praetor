package agentcontext

import (
	"fmt"
	"math/rand/v2"
	"regexp"
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
// nil env, a Seed-shuffled permutation otherwise.
func (e *RenderEnv) order(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	if e == nil {
		return idx
	}
	rng := rand.New(rand.NewPCG(uint64(e.Seed), 0))
	rng.Shuffle(n, func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
	return idx
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

// layoutBands emits the head, the config band and the tail in that order for one vendor
// target, whatever order the source wrote them in. Lines of a vendor section are client
// configuration and always join the config band. Blank lines at a band edge are trimmed and
// the bands are joined by one blank line, so an edit inside a band moves no byte of an
// earlier band.
func layoutBands(lines, owners []string, bands []band, section string) string {
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
	out := make([]string, 0, bandCount)
	for b := range parts {
		if text := trimBlankEdges(parts[b], band(b) == bandTail); text != "" {
			out = append(out, text)
		}
	}
	return strings.Join(out, "\n\n")
}

// blankAfterMarker reports whether line i is the blank line that follows a marker. The marker
// is removed, and the blank line after it goes too, so the marker leaves no double gap.
func blankAfterMarker(lines []string, bands []band, i int) bool {
	return i > 0 && bands[i-1] == bandMarker && strings.TrimSpace(lines[i]) == ""
}

// trimBlankEdges joins lines without blank lines at either edge. The tail keeps its trailing
// newline, so a source that ends in one compiles to a file that ends in one.
func trimBlankEdges(lines []string, keepFinalNewline bool) string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	trailing := end > start+1 && lines[end-1] == ""
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	text := strings.Join(lines[start:end], "\n")
	if keepFinalNewline && trailing && text != "" {
		text += "\n"
	}
	return text
}

// HeadBand returns the head band every vendor file shares (vendor sections never join it) and
// whether content carries band markers. An unmarked source has no head.
func HeadBand(content string) (string, bool) {
	if lf, _, err := util.NormalizeLineEndingsStrict(content); err == nil {
		content = lf
	}
	lines, owners, err := ownVendorLines(content)
	if err != nil {
		return "", false
	}
	bands, marked := scanBands(lines)
	if !marked {
		return "", false
	}
	var head []string
	for i, line := range lines {
		if bands[i] == bandHead && owners[i] == "" {
			head = append(head, line)
		}
	}
	return trimBlankEdges(head, false), true
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

var volatilePatterns = [...]struct {
	kind string
	re   *regexp.Regexp
}{
	{"timestamp", regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2})?(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?`)},
	{"digest", regexp.MustCompile(`(?i)sha256:[0-9a-f]{12,}|\b[0-9a-f]{40,64}\b`)},
	{"absolute path", regexp.MustCompile("(?:^|[\\s`(\"'=])(?:/(?:home|Users|tmp|var|root|mnt|srv|opt)/[^\\s`)\"']+|[A-Za-z]:\\\\[^\\s`)\"']+)")},
	{"run counter", regexp.MustCompile(`(?i)\brun[ _-]?(?:id|counter|count|number)?\s*[#:=]\s*\d+|\bcount[:=]\s*\d+`)},
}

// ScanVolatile returns the volatile tokens in a head band: ISO timestamps, sha256 digests,
// absolute paths and run counters. The head is the prefix a client caches, so one such token
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
