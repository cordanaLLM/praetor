// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// managedTailBlock is a line-oriented file whose last lines adoption owns: a begin marker, the
// canonical lines, an end marker. Every line outside the markers belongs to the operator and
// is kept, in order. The block always moves to the tail, so a later operator rule cannot
// override it, and a re-run replaces it instead of appending a second one, so the file
// converges. .gitignore (gitignore.go) and the formatter inventory (managed_artifacts.go) are
// merged through it; a .gitattributes block takes the same shape.
type managedTailBlock struct {
	// file names the file in errors.
	file string
	// begin and end are the exact marker lines; an indented or padded marker is refused.
	begin, end string
	// maxLines bounds the file this merge reads (HISS-02).
	maxLines int
	// duplicate completes "<file> contains " for a second or nested begin marker.
	duplicate string
	// legacy lists exact unmarked lines a former Praetor format wrote outside any block; they
	// are dropped, so the block supersedes them instead of repeating them.
	legacy []string
}

// render returns the canonical block holding lines between its markers.
func (b managedTailBlock) render(lines []string) string {
	var sb strings.Builder
	sb.WriteString(b.begin + "\n")
	for index := 0; index < len(lines) && index < b.maxLines; index++ {
		sb.WriteString(lines[index] + "\n")
	}
	sb.WriteString(b.end + "\n")
	return sb.String()
}

// merge returns text with block as its tail, after the operator's lines and one blank line.
// text keeps its one consistent line-ending style; mixed endings, a file over maxLines, and
// ambiguous markers are refused rather than guessed at.
func (b managedTailBlock) merge(text, block string) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(text)
	if err != nil {
		return "", fmt.Errorf("%s line endings are inconsistent: %w", b.file, err)
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) > b.maxLines {
		return "", fmt.Errorf("%s exceeds %d lines", b.file, b.maxLines)
	}
	kept, err := b.operatorLines(lines)
	if err != nil {
		return "", err
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if body != "" {
		body += "\n\n"
	}
	return util.RestoreLineEndings(body+block, crlf), nil
}

// tailBlockScan walks a file once, keeping the operator's lines and checking the markers.
type tailBlockScan struct {
	block         managedTailBlock
	kept          []string
	inBlock, seen bool
}

func (b managedTailBlock) operatorLines(lines []string) ([]string, error) {
	scan := tailBlockScan{block: b, kept: make([]string, 0, len(lines))}
	for index := 0; index < len(lines) && index < b.maxLines; index++ {
		if err := scan.consume(lines[index], index); err != nil {
			return nil, err
		}
	}
	if scan.inBlock {
		return nil, fmt.Errorf("%s contains an unterminated managed block", b.file)
	}
	return scan.kept, nil
}

func (scan *tailBlockScan) consume(line string, index int) error {
	b := scan.block
	trimmed := strings.TrimSpace(line)
	if (trimmed == b.begin || trimmed == b.end) && trimmed != line {
		return fmt.Errorf("%s contains a non-canonical managed marker at line %d", b.file, index+1)
	}
	switch {
	case line == b.begin:
		if scan.inBlock || scan.seen {
			return fmt.Errorf("%s contains %s", b.file, b.duplicate)
		}
		scan.inBlock, scan.seen = true, true
	case line == b.end:
		if !scan.inBlock {
			return fmt.Errorf("%s contains an unmatched managed end marker", b.file)
		}
		scan.inBlock = false
	case !scan.inBlock && !slices.Contains(b.legacy, line):
		scan.kept = append(scan.kept, line)
	}
	return nil
}
