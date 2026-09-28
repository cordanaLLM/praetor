// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"regexp"
	"strings"

	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

// CreditsFile is the credits page of the documentation site. Its "Shipped in the binaries" table
// names the npm packages the committed figure player bundles, with their versions, in one row.
const CreditsFile = "docs/credits.md"

// creditsPlayerAnchor marks that row: the player it names as the packages' use.
var creditsPlayerAnchor = "`" + figureassets.Directory + "/dist/player.js`"

// creditsLink matches a Markdown link, whose text alone counts when a row names a package.
var creditsLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// CheckCredits fails when the credits row of the figure player does not name every npm package
// the committed player bundles as "<name> <version>", at the version the figure lock installs
// (figurePlayerRows, the rows RenderNotices writes into NoticesFile). A dependency bump moves the
// lock, the rebuilt player's license file and the notices together; this holds the hand-written
// credits row to the same sources, so it cannot keep naming the outgoing versions.
func CheckCredits(credits string, sources NoticeSources) error {
	components, err := figurePlayerRows(sources.FigureLicenses, sources.FigureLock)
	if err != nil {
		return err
	}
	row, err := creditsPlayerRow(credits)
	if err != nil {
		return err
	}
	named := strings.ToLower(creditsLink.ReplaceAllString(tableCells(row)[0], "$1"))
	var missing []string
	for _, component := range components {
		if !namesTerm(named, strings.ToLower(component.name+" "+component.version)) {
			missing = append(missing, component.name+" "+component.version)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: the row for %s does not name %s; write each package at the version %s installs",
			CreditsFile, creditsPlayerAnchor, strings.Join(missing, ", "), figureLockFile)
	}
	return nil
}

// creditsPlayerRow returns the one table row of credits that names the committed player.
func creditsPlayerRow(credits string) (string, error) {
	lines, err := splitNoticeLines(credits)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", CreditsFile, err)
	}
	var rows []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, creditsPlayerAnchor) {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("%s holds %d table rows naming %s, want exactly one", CreditsFile, len(rows), creditsPlayerAnchor)
	}
	return rows[0], nil
}

// namesTerm reports whether text holds term as a whole term: neither neighbour continues a
// package name or a version, so "react 19.3.0" is not found in "preact 19.3.0" or "react 19.3.01".
func namesTerm(text, term string) bool {
	for offset := 0; offset <= len(text)-len(term) && offset < len(text); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(term)
		if (start == 0 || !termByte(text[start-1])) && (end == len(text) || !termByte(text[end])) {
			return true
		}
		offset = start + 1
	}
	return false
}

// termByte reports whether b can continue a package name or a version.
func termByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '.' || b == '_' || b == '@' || b == '/'
}
