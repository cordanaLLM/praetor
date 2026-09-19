package state

import "strings"

// markdownFence tracks fenced code-block state across a single ordered line
// scan of a ledger file. Every ledger parser that must not mistake an example
// inside a fence for a real record shares this one implementation (HISS-19):
// the bug table parser and the OPEN.md task parser both drive it.
//
// The zero value is a scanner positioned outside any fence.
type markdownFence struct {
	// marker is the opening delimiter of the fence currently being skipped,
	// or the empty string when the scan is outside a fence.
	marker string
}

// open reports whether the scan is currently inside a fenced block.
func (f *markdownFence) open() bool { return f.marker != "" }

// inside advances the tracker by one already-trimmed line and reports whether
// that line is fenced content or a fence delimiter. Callers skip every line for
// which it returns true. It must be called exactly once per line, in order.
func (f *markdownFence) inside(line string) bool {
	if f.marker != "" {
		if strings.HasPrefix(line, f.marker) && strings.Trim(line, string(f.marker[0])+" \t") == "" {
			f.marker = ""
		}
		return true
	}
	if !strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~") {
		return false
	}
	end := 0
	for end < len(line) && line[end] == line[0] {
		end++
	}
	f.marker = line[:end]
	return true
}
