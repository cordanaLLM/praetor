package state

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// CompactReport describes one STATE.md compaction.
type CompactReport struct {
	// Rewritten counts legacy entries rewritten into the compact template.
	Rewritten int `json:"rewritten"`
	// Kept counts entries left as written: already compact, or not engine-shaped.
	Kept int `json:"kept"`
	// MarkersDropped counts superseded sync markers removed.
	MarkersDropped int  `json:"markers_dropped"`
	BytesBefore    int  `json:"bytes_before"`
	BytesAfter     int  `json:"bytes_after"`
	Changed        bool `json:"changed"`
}

var (
	legacyEntryHeading = regexp.MustCompile("^### \\[([^\\]]+)\\] Commit `([^`]*)` on `(.*)`$")
	legacyActivity     = regexp.MustCompile(`^- \*\*Activity\*\*: (.*)$`)
	legacyCounts       = regexp.MustCompile(`^- (?:\*\*Tasks\*\*: (\d+) open, (\d+) completed \| )?\*\*Open Bugs\*\*: (\d+) \| \*\*Pending Questions\*\*: (\d+)$`)
	legacyGit          = regexp.MustCompile(`^- \*\*Git State\*\*: (\S+) \| \*\*Clean\*\*: (true|false) \| \*\*Dirty Paths\*\*: (\d+)$`)
	stateMarkerLine    = regexp.MustCompile(`^<!-- praetor-state:v1 sha256:[a-f0-9]{64} -->$`)
)

// CompactState rewrites legacy STATE.md entries into the compact template once,
// drops superseded sync markers and certifies the result with a fresh sync
// entry in the same write. It refuses a ledger whose last sync no longer
// verifies, so it never certifies changes nobody synchronized. A ledger that is
// already compact is left byte for byte unchanged.
func CompactState(ctx context.Context, rootPath string) (*CompactReport, error) {
	if ctx == nil {
		return nil, errors.New("state compaction requires a context")
	}
	stateFile := filepath.Join(rootPath, WorkingDirName, "STATE.md")
	content, err := contextopt.ReadSnapshot(ctx, stateFile)
	if err != nil {
		return nil, fmt.Errorf("read STATE.md: %w", err)
	}
	if err := verifyStateContent(ctx, rootPath, content); err != nil {
		return nil, fmt.Errorf("state compact refuses an unverified ledger: %w", err)
	}
	body := supersededBase(content)
	compacted, report, err := compactStateBody(body)
	if err != nil {
		return nil, err
	}
	report.BytesBefore, report.BytesAfter = len(content), len(content)
	if compacted == body {
		return &report, nil
	}
	summary := fmt.Sprintf("compact %d entries, %d markers dropped", report.Rewritten, report.MarkersDropped)
	written, err := certifyStateRewrite(ctx, rootPath, content, compacted, summary)
	if err != nil {
		return nil, err
	}
	report.BytesAfter, report.Changed = written, true
	return &report, VerifyStateSync(ctx, rootPath)
}

// certifyStateRewrite replaces STATE.md with base plus a fresh sync entry.
func certifyStateRewrite(ctx context.Context, rootPath string, expected []byte, base, summary string) (int, error) {
	snap, err := InspectState(ctx, rootPath)
	if err != nil {
		return 0, err
	}
	snap.StateHash, err = stateBinding(ctx, rootPath, snap)
	if err != nil {
		return 0, err
	}
	stateFile := filepath.Join(rootPath, WorkingDirName, "STATE.md")
	return writeStateEntry(ctx, stateFile, expected, base, snap, summary)
}

// compactStateBody rewrites the log before the last sync marker. Output is
// canonical: the preamble, then entries separated by one blank line, every line
// newline-terminated. Canonical output is a fixed point, which makes a second
// compaction a no-op.
func compactStateBody(body string) (string, CompactReport, error) {
	var report CompactReport
	if body == "" {
		return "", report, nil
	}
	lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n")
	if len(lines) > maxScannedLines {
		return "", report, fmt.Errorf("STATE.md exceeds %d lines", maxScannedLines)
	}
	preamble, blocks := stateBlocks(lines, &report)
	parts := make([]string, 0, len(blocks)+1)
	if kept := trimBlankTail(preamble); len(kept) > 0 {
		parts = append(parts, strings.Join(kept, "\n")+"\n")
	}
	for _, block := range blocks {
		parts = append(parts, compactBlock(trimBlankTail(block), &report))
	}
	return strings.Join(parts, "\n"), report, nil
}

// stateBlocks splits log lines into the preamble and one block per `### [`
// heading, dropping superseded sync markers wherever they stand.
func stateBlocks(lines []string, report *CompactReport) (preamble []string, blocks [][]string) {
	for _, line := range lines {
		switch {
		case stateMarkerLine.MatchString(line):
			report.MarkersDropped++
		case isEntryHeading(line):
			blocks = append(blocks, []string{line})
		case len(blocks) == 0:
			preamble = append(preamble, line)
		default:
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], line)
		}
	}
	return preamble, blocks
}

// isEntryHeading reports whether a STATE.md line opens a log entry. Compaction and history
// rotation both split the log on it, so they always agree on where an entry starts.
func isEntryHeading(line string) bool {
	return strings.HasPrefix(line, "### [")
}

func trimBlankTail(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[:end]
}

// compactBlock rewrites one engine-shaped legacy entry; anything else is kept
// line for line, so an entry this parser does not recognise is never lost.
func compactBlock(lines []string, report *CompactReport) string {
	if entry, ok := parseLegacyEntry(lines); ok {
		report.Rewritten++
		return entry.render()
	}
	report.Kept++
	return strings.Join(lines, "\n") + "\n"
}

// parseLegacyEntry reads the labelled-bullet entry that sync wrote before the
// compact template: heading, activity, counts and optionally Git state.
func parseLegacyEntry(lines []string) (stateEntry, bool) {
	if len(lines) < 3 || len(lines) > 4 {
		return stateEntry{}, false
	}
	heading := legacyEntryHeading.FindStringSubmatch(lines[0])
	activity := legacyActivity.FindStringSubmatch(lines[1])
	counts := legacyCounts.FindStringSubmatch(lines[2])
	if heading == nil || activity == nil || counts == nil {
		return stateEntry{}, false
	}
	numbers, ok := parseCounts(counts[1:])
	if !ok {
		return stateEntry{}, false
	}
	entry := stateEntry{Time: heading[1], Head: heading[2], Branch: heading[3], Activity: activity[1],
		Bugs: numbers[2], Questions: numbers[3]}
	if counts[1] != "" {
		entry.Tasks = &[2]int{numbers[0], numbers[1]}
	}
	if len(lines) == 3 {
		return entry, true
	}
	entry.Git, ok = parseLegacyGit(lines[3])
	return entry, ok
}

// parseCounts converts optional decimal fields; an absent field reads as zero.
func parseCounts(fields []string) ([]int, bool) {
	numbers := make([]int, len(fields))
	for i, field := range fields {
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, false
		}
		numbers[i] = n
	}
	return numbers, true
}

// parseLegacyGit reads the Git bullet. A clean tree with dirty paths is not
// something sync writes, so it stays verbatim rather than lose a count.
func parseLegacyGit(line string) (*stateGitEntry, bool) {
	match := legacyGit.FindStringSubmatch(line)
	if match == nil {
		return nil, false
	}
	dirty, err := strconv.Atoi(match[3])
	clean := match[2] == "true"
	if err != nil || clean && dirty != 0 {
		return nil, false
	}
	return &stateGitEntry{State: match[1], Clean: clean, Dirty: dirty}, true
}
