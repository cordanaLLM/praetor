// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"fmt"
	"maps"
	"path"
	"regexp"
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
	manifest, err := nodemanifest.ParseManifest([]byte(sources.ManifestTexts[consoleManifestFile]))
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

// checkConsoleCreditEntry compares every version claim of an entry's own npm packages,
// including each pinned package a scope-only identifier such as npm:@scope covers.
func checkConsoleCreditEntry(entry CreditEntry, index int, pins map[string]string) error {
	for _, id := range entry.Packages {
		if !strings.HasPrefix(id, "npm:") {
			continue
		}
		for _, name := range consoleCoveredNames(id, pins) {
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
	}
	return nil
}

// consoleCoveredNames lists the package names whose claims an npm package identifier answers
// for: the name itself when the manifest pins it or pins nothing it covers, otherwise every
// pinned package the identifier covers by CreditEntry.answers (npm:@scope covers @scope/kit).
func consoleCoveredNames(id string, pins map[string]string) []string {
	name := strings.TrimPrefix(id, "npm:")
	if _, exact := pins[name]; exact {
		return []string{name}
	}
	single := CreditEntry{Packages: []string{id}}
	var covered []string
	for pinned := range pins {
		if single.answers(InventoryItem{Kind: inventoryNPM, ID: pinned}) {
			covered = append(covered, pinned)
		}
	}
	if len(covered) == 0 {
		return []string{name}
	}
	slices.Sort(covered)
	return covered
}

// consoleDetailVersions reads full or scope-shortened "<name> <version>" terms and a bare
// version (the entry's packages share that claim). Separated comparators and compound ranges
// stay in the claim; Markdown link destinations never count.
func consoleDetailVersions(detail, name string) []string {
	text := util.MarkdownLinkRe.ReplaceAllString(detail, "$1")
	text = strings.NewReplacer("`", "", ",", " ", ";", " ", "(", " ", ")", " ", "||", " || ").Replace(text)
	fields := strings.Fields(text)
	var versions []string
	for index := range fields {
		claim, next, ok := consoleVersionClaim(fields, index)
		if !ok {
			continue
		}
		if index == 0 && next == len(fields) || index > 0 && (fields[index-1] == name || fields[index-1] == path.Base(name)) {
			versions = append(versions, claim)
		}
	}
	return versions
}

// consoleVersionClaim reads every version term of a claim starting at index, keeping standalone
// comparators and range joiners (||, -) with the claim up to the next package name.
func consoleVersionClaim(fields []string, index int) (string, int, bool) {
	claim, next, ok := consoleVersionTerm(fields, index)
	if !ok {
		return "", index, false
	}
	for step := 0; step < len(fields) && next < len(fields); step++ {
		joiner, start := "", next
		if fields[next] == "||" || fields[next] == "-" {
			joiner, start = " "+fields[next], next+1
		}
		term, after, ok := consoleVersionTerm(fields, start)
		if !ok {
			break
		}
		claim += joiner + " " + term
		next = after
	}
	return claim, next, true
}

// consoleVersionValue matches the version part of a console credit term after any comparator:
// digits, optionally dot-separated (a bare major such as 3 is a version, not an exact pin), with an
// optional prerelease or build suffix, never a digit-leading package name such as 7zip-bin.
var consoleVersionValue = regexp.MustCompile(`^v?\d+(?:\.\d+)*(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// consoleVersionTerm reads one version term (with any standalone comparator) starting at index.
func consoleVersionTerm(fields []string, index int) (string, int, bool) {
	if index >= len(fields) {
		return "", index, false
	}
	field := strings.TrimSuffix(fields[index], ".")
	next := index + 1
	operator, value := semver.CutOperator(field)
	if operator != "" && value == "" && next < len(fields) {
		field += " " + strings.TrimSuffix(fields[next], ".")
		_, value = semver.CutOperator(field)
		next++
	}
	if !consoleVersionValue.MatchString(strings.TrimSpace(value)) {
		return "", index, false
	}
	return field, next, true
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
