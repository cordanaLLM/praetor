// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package readmepresentation checks an adopted repository's README.md against the shared
// README presentation standard (ADR-3366, rules README-01..README-05).
package readmepresentation

import (
	"fmt"
)

const (
	// RuleIDAbsent identifies an absent README.md file.
	RuleIDAbsent = "README-00"
	// RuleIDBadgeBlock identifies badge block placement and group ordering violations.
	RuleIDBadgeBlock = "README-01"
	// RuleIDTagline identifies tagline placement and formatting violations.
	RuleIDTagline = "README-02"
	// RuleIDHero identifies hero banner sizing and styling violations.
	RuleIDHero = "README-03"
	// RuleIDDiagrams identifies unthemed mermaid blocks or oversized SVGs.
	RuleIDDiagrams = "README-04"
	// RuleIDSections identifies duplicate H2 headings or section order violations.
	RuleIDSections = "README-05"

	// GateName is the name printed on audit report lines.
	GateName = "README presentation"
	// FileName is the repository README checked by this gate.
	FileName = "README.md"

	// MaxFileBytes bounds the README read (HISS-02).
	MaxFileBytes = 1 << 20 // 1 MiB
	// MaxLines bounds the number of lines scanned (HISS-02).
	MaxLines = 5000
	// MaxSVGBytes bounds an SVG file read for its viewBox (HISS-02).
	MaxSVGBytes = 1 << 20 // 1 MiB
	// HeroMaxHeight is the maximum permitted hero banner height attribute.
	HeroMaxHeight = 120
	// SVGMaxWidth is the maximum permitted viewBox width for figures below header.
	SVGMaxWidth = 830
)

// Finding describes one rule violation detected in README.md.
type Finding struct {
	File    string
	Line    int
	RuleID  string
	Message string
}

// String formats the finding as a file:line locator and diagnostic.
func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.RuleID, f.Message)
}
