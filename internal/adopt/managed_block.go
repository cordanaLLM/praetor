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
// converges. .gitignore (gitignore.go), the formatter inventory (managed_artifacts.go) and
// .gitattributes (gitattributes.go) are merged through it.
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
	body, _, crlf, err := b.split(text)
	if err != nil {
		return "", err
	}
	if body != "" {
		body += "\n\n"
	}
	return util.RestoreLineEndings(body+block, crlf), nil
}

// strip returns text without the block: the operator's lines, in order and in the file's
// line-ending style, and removed reports whether there was a block. It refuses what merge
// refuses, and text without a block comes back unchanged.
func (b managedTailBlock) strip(text string) (stripped string, removed bool, err error) {
	body, removed, crlf, err := b.split(text)
	if err != nil || !removed {
		return text, false, err
	}
	if body != "" {
		body += "\n"
	}
	return util.RestoreLineEndings(body, crlf), true, nil
}

// split returns the operator's lines of text joined with LF and without trailing newlines,
// whether text held the block, and whether text used CRLF endings.
func (b managedTailBlock) split(text string) (body string, held, crlf bool, err error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(text)
	if err != nil {
		return "", false, false, fmt.Errorf("%s line endings are inconsistent: %w", b.file, err)
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) > b.maxLines {
		return "", false, false, fmt.Errorf("%s exceeds %d lines", b.file, b.maxLines)
	}
	kept, held, err := b.operatorLines(lines)
	if err != nil {
		return "", false, false, err
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n"), held, crlf, nil
}

// tailBlockScan walks a file once, keeping the operator's lines and checking the markers.
type tailBlockScan struct {
	block         managedTailBlock
	kept          []string
	inBlock, seen bool
}

// operatorLines returns the lines outside the block and whether the block was found.
func (b managedTailBlock) operatorLines(lines []string) ([]string, bool, error) {
	scan := tailBlockScan{block: b, kept: make([]string, 0, len(lines))}
	for index := 0; index < len(lines) && index < b.maxLines; index++ {
		if err := scan.consume(lines[index], index); err != nil {
			return nil, false, err
		}
	}
	if scan.inBlock {
		return nil, false, fmt.Errorf("%s contains an unterminated managed block", b.file)
	}
	return scan.kept, scan.seen, nil
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
