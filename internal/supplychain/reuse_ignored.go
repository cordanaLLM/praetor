// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"regexp"
	"strings"
)

// The reuse tool never reads some files and directories (reuse 6.2.0, reuse/covered_files.py,
// _IGNORE_FILE_PATTERNS and _IGNORE_DIR_PATTERNS): a licence text named LICENSE or COPYING, a
// .license sidecar, REUSE.toml itself, SPDX documents, and the .git, .hg, .sl, LICENSES and .reuse
// directories. A REUSE.toml annotation naming such a file labels nothing: reuse lint neither checks
// it nor counts its licence as used, so the holder the table names reaches no record (#1287, the
// skill LICENSE tables). reuseIgnoredFiles and reuseIgnoredDirs copy those patterns.
var (
	reuseIgnoredFiles = []*regexp.Regexp{
		regexp.MustCompile(`^LICEN[CS]E([-.].*)?$`),
		regexp.MustCompile(`^COPYING([-.].*)?$`),
		regexp.MustCompile(`^\.git$`),
		regexp.MustCompile(`^\.hgtags$`),
		regexp.MustCompile(`^.*\.license$`),
		regexp.MustCompile(`^REUSE\.toml$`),
		regexp.MustCompile(`^CAL-1.0(-Combined-Work-Exception)?(\..+)?$`),
		regexp.MustCompile(`^SHL-2.1(\..+)?$`),
		regexp.MustCompile(`^.*\.spdx$`),
		regexp.MustCompile(`^.*\.spdx.(rdf|json|xml|ya?ml)$`),
	}
	reuseIgnoredDirs = []*regexp.Regexp{
		regexp.MustCompile(`^\.git$`),
		regexp.MustCompile(`^\.hg$`),
		regexp.MustCompile(`^\.sl$`),
		regexp.MustCompile(`^LICENSES$`),
		regexp.MustCompile(`^\.reuse$`),
	}
)

// ReuseIgnoredPath is one path glob of a REUSE.toml annotation table that names only files the
// reuse tool never reads.
type ReuseIgnoredPath struct {
	// Table and Path are the table, numbered from 1 in file order, and its glob.
	Table int
	Path  string
	// Name is the literal file or directory name that reuse skips.
	Name string
}

// String names the glob and why it labels nothing.
func (p ReuseIgnoredPath) String() string {
	return fmt.Sprintf("%s annotation %d path %q labels nothing: the reuse tool never reads %q, so its holder and "+
		"licence reach no record and reuse lint counts the licence as unused; record the notice in the file itself or "+
		"in a notices file, and remove the path", ReuseFile, p.Table, p.Path, p.Name)
}

// ReuseIgnoredPaths returns every path glob of tables whose files the reuse tool never reads: a
// glob whose last segment is a literal name reuse skips (LICENSE, COPYING, a .license sidecar),
// or one with a literal directory segment reuse skips (LICENSES, .git). A segment holding a "*"
// can match other names too and is not reported.
func ReuseIgnoredPaths(tables []ReuseAnnotation) []ReuseIgnoredPath {
	var ignored []ReuseIgnoredPath
	for table := 0; table < len(tables) && table < MaxReuseLines; table++ {
		for _, glob := range tables[table].Paths {
			if name, ok := reuseIgnoredName(glob); ok {
				ignored = append(ignored, ReuseIgnoredPath{Table: table + 1, Path: glob, Name: name})
			}
		}
	}
	return ignored
}

// reuseIgnoredName returns the literal name of glob that the reuse tool skips, if any.
func reuseIgnoredName(glob string) (string, bool) {
	segments := reuseLiteralSegments(glob)
	for index, segment := range segments {
		if segment == "" {
			continue
		}
		patterns := reuseIgnoredDirs
		if index == len(segments)-1 {
			patterns = reuseIgnoredFiles
		}
		for _, pattern := range patterns {
			if pattern.MatchString(segment) {
				return segment, true
			}
		}
	}
	return "", false
}

// reuseLiteralSegments splits glob at its "/" and returns each segment's literal text, or "" for
// a segment holding a "*" or "**". A "**/" ends its segment with the "/" it spans.
func reuseLiteralSegments(glob string) []string {
	var segments []string
	var current strings.Builder
	literal := true
	// Every token spans at least one byte, so len(glob) passes read the whole glob.
	for index := 0; index < len(glob); {
		token, width := reuseGlobNext(glob[index:])
		closes := token.kind == reuseGlobLiteral && token.char == '/' && glob[index] != '\\' ||
			token.kind == reuseGlobstar && strings.HasSuffix(glob[index:index+width], "/")
		switch {
		case token.kind != reuseGlobLiteral:
			literal = false
		case !closes:
			current.WriteByte(token.char)
		}
		if closes {
			segments = append(segments, reuseSegmentText(current.String(), literal))
			current.Reset()
			literal = true
		}
		index += width
	}
	return append(segments, reuseSegmentText(current.String(), literal))
}

func reuseSegmentText(text string, literal bool) string {
	if !literal {
		return ""
	}
	return text
}
