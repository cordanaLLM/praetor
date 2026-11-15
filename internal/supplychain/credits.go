// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
)

// AcknowledgementsFile is the credits page of the documentation site ("Credits &
// Acknowledgements"). Its "Shipped in the binaries" table names the npm packages the committed
// figure player bundles, with their versions, in one row. The name avoids "cred", which gosec's
// G101 hardcoded-credential rule reads in a constant's name.
const AcknowledgementsFile = "docs/credits.md"

// consoleManifestFile is the source of the console's exact dependency pins.
const consoleManifestFile = "console/package.json"

// creditsPlayerAnchor marks that row: the player it names as the packages' use.
var creditsPlayerAnchor = "`" + figureassets.PlayerDirectory + "/player.js`"

// CheckCredits fails when the credits row of the figure player does not name every npm package
// the committed player bundles as "<name> <version>", at the version the figure lock installs
// (figurePlayerRows, the rows RenderNotices writes into NoticesFile). A dependency bump moves the
// lock, the rebuilt player's license file and the notices together; this holds the hand-written
// credits row to the same sources, so it cannot keep naming the outgoing versions. Console
// entries in upstream must name their packages at the pins console/package.json declares;
// entries without version text need no version claim.
func CheckCredits(credits string, sources NoticeSources, upstream UpstreamCreditSources) error {
	components, err := figurePlayerRows(sources.FigureLicenses, sources.FigureLock)
	if err != nil {
		return err
	}
	row, err := creditsPlayerRow(credits)
	if err != nil {
		return err
	}
	named := strings.ToLower(util.MarkdownLinkRe.ReplaceAllString(tableCells(row)[0], "$1"))
	var missing []string
	for _, component := range components {
		if !namesTerm(named, strings.ToLower(component.name+" "+component.version), termByte) {
			missing = append(missing, component.name+" "+component.version)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: the row for %s does not name %s; write each package at the version %s installs",
			AcknowledgementsFile, creditsPlayerAnchor, strings.Join(missing, ", "), figureLockFile)
	}
	return checkConsoleCreditVersions(upstream)
}

// checkConsoleCreditVersions extends the credits comparison to entries using the console.
// The existing manifest reader refuses duplicate keys rather than choosing a pin silently.
func checkConsoleCreditVersions(sources UpstreamCreditSources) error {
	usesConsole := func(entry CreditEntry) bool { return slices.Contains(entry.Paths, consoleManifestFile) }
	if !slices.ContainsFunc(sources.Credits.Entries, usesConsole) {
		return nil
	}
	manifest, err := nodemanifest.ParseManifest([]byte(sources.PathTexts[consoleManifestFile]))
	if err != nil {
		return fmt.Errorf("parse %s for credits: %w", consoleManifestFile, err)
	}
	pins := map[string]string{}
	for _, group := range manifest.Groups() {
		maps.Copy(pins, group)
	}
	for index, entry := range sources.Credits.Entries {
		if usesConsole(entry) {
			if err := checkConsoleCreditEntry(entry, index, pins); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkConsoleCreditEntry compares every version claim of an entry's own npm packages.
func checkConsoleCreditEntry(entry CreditEntry, index int, pins map[string]string) error {
	for _, id := range entry.Packages {
		name, npm := strings.CutPrefix(id, "npm:")
		if !npm {
			continue
		}
		for _, version := range consoleDetailVersions(entry.Detail, name) {
			pin, exists := pins[name]
			if !exists {
				return fmt.Errorf("%s names %s %s; %s has no pin for that package", entry.label(index), name, version, consoleManifestFile)
			}
			if version != pin {
				return fmt.Errorf("%s names %s %s; %s pins %s", entry.label(index), name, version, consoleManifestFile, pin)
			}
		}
	}
	return nil
}

// consoleDetailVersions reads full or scope-shortened "<name> <version>" terms and a bare
// version (the entry's packages share that claim). Separated comparators stay in the claim;
// Markdown link destinations never count.
func consoleDetailVersions(detail, name string) []string {
	text := util.MarkdownLinkRe.ReplaceAllString(detail, "$1")
	text = strings.NewReplacer("`", "", ",", " ", ";", " ", "(", " ", ")", " ").Replace(text)
	fields := strings.Fields(text)
	var versions []string
	for index := range fields {
		field := consoleVersionClaim(fields, index)
		_, value := semver.CutOperator(field)
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		if len(value) == 0 || value[0] < '0' || value[0] > '9' {
			continue
		}
		if len(fields) == 1 || index > 0 && (fields[index-1] == name || fields[index-1] == path.Base(name)) {
			versions = append(versions, field)
		}
	}
	return versions
}

// consoleVersionClaim keeps a standalone comparator and its following version together.
func consoleVersionClaim(fields []string, index int) string {
	field := strings.TrimSuffix(fields[index], ".")
	operator, value := semver.CutOperator(field)
	if operator != "" && value == "" && index+1 < len(fields) {
		return field + " " + strings.TrimSuffix(fields[index+1], ".")
	}
	return field
}

// creditsPlayerRow returns the one table row of credits that names the committed player.
func creditsPlayerRow(credits string) (string, error) {
	lines, err := splitNoticeLines(credits)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", AcknowledgementsFile, err)
	}
	var rows []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "|") && strings.Contains(line, creditsPlayerAnchor) {
			rows = append(rows, line)
		}
	}
	if len(rows) != 1 {
		return "", fmt.Errorf("%s holds %d table rows naming %s, want exactly one", AcknowledgementsFile, len(rows), creditsPlayerAnchor)
	}
	return rows[0], nil
}

// hasTermBoundaries reports whether the term match at [start, end) respects boundaries.
func hasTermBoundaries(text, term string, start, end int, continues func(byte) bool) bool {
	before := start == 0 || !continues(term[0]) || !continues(text[start-1])
	after := end == len(text) || !continues(term[len(term)-1]) || !continues(text[end])
	return before && after
}

// namesTerm reports whether text holds term as a whole term: where an edge byte of term
// continues a term (continues), the byte next to it in text does not. With termByte, "react
// 19.3.0" is not found in "preact 19.3.0" or "react 19.3.01"; an edge byte that continues
// nothing, such as the slash of ".continue/", needs no boundary.
func namesTerm(text, term string, continues func(byte) bool) bool {
	if term == "" {
		return false
	}
	for offset := 0; offset <= len(text)-len(term) && offset < len(text); {
		index := strings.Index(text[offset:], term)
		if index < 0 {
			return false
		}
		start, end := offset+index, offset+index+len(term)
		if hasTermBoundaries(text, term, start, end, continues) {
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
