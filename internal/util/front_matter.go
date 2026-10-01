// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// frontMatterFence opens and closes a YAML front matter block.
const frontMatterFence = "---"

// FrontMatterEnd reads the YAML front matter block that opens lines: a first line "---" and
// the next line "---", either followed by nothing but spaces, tabs or a carriage return. It
// returns the index of the first line after the block, or 0 when there is none, and reports
// whether the first line opens one, so a block that is never closed within the first maxLines
// lines is told apart from a document without one (end 0, opened true). markdownlint skips such
// a block (internal/testsupport), and an agent skill or persona declares its metadata in it
// (internal/compiler, CanonicalAssetUpstreams).
func FrontMatterEnd(lines []string, maxLines int) (end int, opened bool) {
	if len(lines) == 0 || !isFrontMatterFence(lines[0]) {
		return 0, false
	}
	for index := 1; index < len(lines) && index < maxLines; index++ {
		if isFrontMatterFence(lines[index]) {
			return index + 1, true
		}
	}
	return 0, true
}

// isFrontMatterFence reports whether line is "---" with nothing after it but blanks.
func isFrontMatterFence(line string) bool {
	return strings.TrimRight(line, " \t\r") == frontMatterFence
}
