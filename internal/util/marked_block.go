package util

import (
	"errors"
	"fmt"
	"strings"
)

// MaxMarkedBlockLines bounds the document a marked-block scan will walk (HISS-02).
const MaxMarkedBlockLines = 1 << 16

var (
	// ErrMarkedBlockArguments is returned for an empty marker, identical markers or a
	// non-positive line budget.
	ErrMarkedBlockArguments = errors.New("util: marked block requires two distinct markers and a positive line budget")
	// ErrMarkedBlockUnbalanced is returned when one marker is present without the other,
	// or the end marker precedes the start marker.
	ErrMarkedBlockUnbalanced = errors.New("util: marked block markers are unbalanced")
	// ErrMarkedBlockDuplicated is returned when either marker appears more than once.
	ErrMarkedBlockDuplicated = errors.New("util: marked block markers are duplicated")
	// ErrMarkedBlockBudget is returned when the document exceeds its line budget.
	ErrMarkedBlockBudget = errors.New("util: marked block document exceeds its line budget")
)

// fenceTracker follows Markdown fenced code so that a marker quoted inside an example is
// never mistaken for the live one.
type fenceTracker struct{ open string }

// inside reports whether trimmed belongs to fenced code, the fence lines included.
func (f *fenceTracker) inside(trimmed string) bool {
	if f.open != "" {
		if strings.HasPrefix(trimmed, f.open) && strings.Trim(trimmed, f.open[:1]) == "" {
			f.open = ""
		}
		return true
	}
	if !strings.HasPrefix(trimmed, "```") && !strings.HasPrefix(trimmed, "~~~") {
		return false
	}
	run := len(trimmed) - len(strings.TrimLeft(trimmed, trimmed[:1]))
	f.open = trimmed[:run]
	return true
}

// FindMarkedBlock locates the single start..end span of a tool-written region in a
// hand-edited Markdown document. Markers are compared after trimming whitespace and are
// ignored inside fenced code. It returns the zero-based line indexes of both markers, or
// (-1, -1) when neither marker is present.
func FindMarkedBlock(content, start, end string) (first, last int, err error) {
	if !distinctMarkers(start, end) {
		return -1, -1, ErrMarkedBlockArguments
	}
	lines := strings.Split(content, "\n")
	if len(lines) > MaxMarkedBlockLines {
		return -1, -1, fmt.Errorf("%w: more than %d lines", ErrMarkedBlockBudget, MaxMarkedBlockLines)
	}
	starts, ends := markerLines(lines, start, end)
	switch {
	case len(starts) > 1 || len(ends) > 1:
		return -1, -1, fmt.Errorf("%w: %s .. %s", ErrMarkedBlockDuplicated, start, end)
	case len(starts) == 0 && len(ends) == 0:
		return -1, -1, nil
	case len(starts) != len(ends) || ends[0] < starts[0]:
		return -1, -1, fmt.Errorf("%w: %s .. %s", ErrMarkedBlockUnbalanced, start, end)
	}
	return starts[0], ends[0], nil
}

func distinctMarkers(start, end string) bool {
	return start != "" && end != "" && start != end
}

// markerLines returns the line indexes of each marker outside fenced code.
func markerLines(lines []string, start, end string) (starts, ends []int) {
	fence := fenceTracker{}
	for i := 0; i < len(lines) && i < MaxMarkedBlockLines; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if fence.inside(trimmed) {
			continue
		}
		if trimmed == start {
			starts = append(starts, i)
		}
		if trimmed == end {
			ends = append(ends, i)
		}
	}
	return starts, ends
}

// ReplaceMarkedBlock replaces the start..end span of content, markers included, with
// body, or appends a blank line and body when the document carries no markers yet. It
// reports whether the document changed and refuses a result longer than maxLines.
//
// It differs from the milestone ledger's block handling on purpose: that code drops a span
// and tolerates a missing end marker, while a canonical agent context must fail loudly on
// an unterminated or duplicated marker instead of swallowing the rest of the file.
func ReplaceMarkedBlock(content, start, end, body string, maxLines int) (out string, changed bool, err error) {
	if maxLines < 1 {
		return "", false, ErrMarkedBlockArguments
	}
	first, last, err := FindMarkedBlock(content, start, end)
	if err != nil {
		return "", false, err
	}
	body = strings.TrimRight(body, "\n")
	switch {
	case first >= 0:
		lines := strings.Split(content, "\n")
		merged := append(append(append([]string{}, lines[:first]...), body), lines[last+1:]...)
		out = strings.Join(merged, "\n")
	case strings.TrimSpace(content) == "":
		out = body + "\n"
	default:
		out = strings.TrimRight(content, "\n") + "\n\n" + body + "\n"
	}
	if count := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; count > maxLines {
		return "", false, fmt.Errorf("%w: %d lines, budget %d", ErrMarkedBlockBudget, count, maxLines)
	}
	return out, out != content, nil
}
