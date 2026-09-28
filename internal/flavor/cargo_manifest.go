// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// cargoManifest is what one Cargo.toml declares about crate editions and workspace members, as
// parseCargoManifest reads it.
type cargoManifest struct {
	// hasPackage reports a [package] table: the manifest describes a crate.
	hasPackage bool
	// edition is the well-formed edition (rustEdition) [package] declares, "" for none.
	edition string
	// inherits reports that [package] takes the edition [workspace.package] declares, as
	// edition.workspace = true or edition = { workspace = true }.
	inherits bool
	// workspaceEdition is the well-formed edition [workspace.package] declares, "" for none.
	workspaceEdition string
	// members and exclude are the path lists [workspace] declares.
	members, exclude []string
	// invalid says why a members or exclude value could not be read, "" when both could.
	invalid string
}

// cargoScan is parseCargoManifest's position in the manifest.
type cargoScan struct {
	manifest cargoManifest
	// table is the full name of the table the lines being read belong to, "" at the top level.
	table string
	// listKey is the members or exclude key whose array is still open, "" when none is.
	listKey string
	// listItems holds the strings read so far from the array listKey opened.
	listItems []string
}

// parseCargoManifest reads the edition and workspace keys of a Cargo.toml. Like the other TOML
// reads of the module (internal/util/toml.go) it reads the file's shape, not TOML: table
// headers, single-line keys, dotted or under their table, and the arrays members and exclude
// hold, which may span lines. Cargo parses the file and rejects a malformed one.
func parseCargoManifest(text string) cargoManifest {
	var scan cargoScan
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		scan.read(strings.TrimSpace(lines[i]))
	}
	if scan.listKey != "" {
		scan.manifest.invalid = scan.listKey + " in Cargo.toml never closes its array"
	}
	return scan.manifest
}

// read takes one trimmed line: the next part of an open array, a table header, or a key.
func (s *cargoScan) read(line string) {
	switch {
	case s.listKey != "":
		// No element spans lines, so each line of an open array reads as an array of its own.
		s.readList("[" + line)
	case strings.HasPrefix(line, "["):
		s.table = util.TOMLTableName(line)
		s.manifest.hasPackage = s.manifest.hasPackage || s.table == "package"
	case line != "" && !strings.HasPrefix(line, "#"):
		s.assign(line)
	}
}

// assign records the key line assigns, when it is one parseCargoManifest reads.
func (s *cargoScan) assign(line string) {
	key, value, ok := util.TOMLKeyValue(line)
	if !ok {
		return
	}
	if s.table != "" {
		key = s.table + "." + key
	}
	s.manifest.hasPackage = s.manifest.hasPackage || strings.HasPrefix(key, "package.")
	switch key {
	case "package.edition":
		s.manifest.edition = editionValue(value)
		s.manifest.inherits = s.manifest.inherits || inheritsWorkspace(value)
	case "package.edition.workspace":
		s.manifest.inherits = s.manifest.inherits || tomlTrue(value)
	case "workspace.package.edition":
		s.manifest.workspaceEdition = editionValue(value)
	case "workspace.members", "workspace.exclude":
		s.listKey, s.listItems = key, nil
		s.readList(value)
	}
}

// readList reads text, one line of the array listKey opened, and records the array once it
// closes.
func (s *cargoScan) readList(text string) {
	items, closed, ok := util.TOMLStringArray(text + "\n")
	switch {
	case !ok:
		s.manifest.invalid = s.listKey + " in Cargo.toml is not an array of plain strings"
		s.listKey = ""
	case closed && s.listKey == "workspace.members":
		s.manifest.members, s.listKey = append(s.listItems, items...), ""
	case closed:
		s.manifest.exclude, s.listKey = append(s.listItems, items...), ""
	default:
		s.listItems = append(s.listItems, items...)
	}
}

// editionValue returns the edition value assigns when it is a well-formed edition string
// (rustEdition), else "".
func editionValue(value string) string {
	edition, ok := util.TOMLStringValue(value)
	if !ok || !rustEdition.MatchString(edition) {
		return ""
	}
	return edition
}

// inheritsWorkspace reports whether value is an inline table setting workspace = true, the
// edition = { workspace = true } spelling of an inherited edition.
func inheritsWorkspace(value string) bool {
	inherits := false
	util.TOMLInlineTableFields(value, func(key, fieldValue string) {
		inherits = inherits || (key == "workspace" && tomlTrue(fieldValue))
	})
	return inherits
}

// tomlTrue reports whether value is the boolean true, a comment after it aside.
func tomlTrue(value string) bool {
	value, _, _ = strings.Cut(value, "#")
	return strings.TrimSpace(value) == "true"
}
