// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// pytestConfigFile is one file pytest reads its configuration from. A file with no table or
// section names is configuration whenever it exists, even empty; any other file is configuration
// only when it holds one of the named TOML tables or INI sections.
type pytestConfigFile struct {
	name     string
	tables   []string
	sections []string
	// optional marks a file that is verification input only as possible pytest configuration:
	// tox.ini and setup.cfg also configure tools that are not pytest, in repositories with no
	// Python at all. One the walk cannot read within its bounds is not pytest configuration, and
	// the report names it, instead of failing adoption (verificationInputs.captureMarker).
	optional bool
}

// pytestConfigFiles lists the files pytest reads its configuration from, at the repository root,
// in pytest's own precedence order: pytest.toml and .pytest.toml (pytest 9.0), pytest.ini and
// .pytest.ini, then pyproject.toml with a [tool.pytest] (pytest 9.0) or [tool.pytest.ini_options]
// (pytest 6.0) table, tox.ini with a [pytest] section and setup.cfg with a [tool:pytest] section
// (https://docs.pytest.org/en/stable/reference/customize.html). pytest also falls back to a
// pyproject.toml without a pytest table as its rootdir anchor; that selects no test gate, so it is
// not configuration here (#594).
var pytestConfigFiles = [...]pytestConfigFile{
	{name: "pytest.toml"},
	{name: ".pytest.toml"},
	{name: "pytest.ini"},
	{name: ".pytest.ini"},
	{name: "pyproject.toml", tables: []string{"tool.pytest.ini_options", "tool.pytest"}},
	{name: "tox.ini", sections: []string{"pytest"}, optional: true},
	{name: "setup.cfg", sections: []string{"tool:pytest"}, optional: true},
}

// pytestConfigFileAt returns the entry of pytestConfigFiles for the root file rel.
func pytestConfigFileAt(rel string) (pytestConfigFile, bool) {
	for _, candidate := range pytestConfigFiles {
		if candidate.name == rel {
			return candidate, true
		}
	}
	return pytestConfigFile{}, false
}

// pytestConfigMarker reports a root file pytestConfigFiles names, which the verification walk
// records for pytestConfiguration.
func pytestConfigMarker(rel string) bool {
	_, found := pytestConfigFileAt(rel)
	return found
}

// pytestPresenceMarker reports a pytest configuration file whose presence alone makes it pytest's
// configuration, so the walk records it without reading it.
func pytestPresenceMarker(rel string) bool {
	candidate, found := pytestConfigFileAt(rel)
	return found && len(candidate.tables) == 0 && len(candidate.sections) == 0
}

// pytestOptionalMarker reports a pytest configuration file the walk reads only as possible pytest
// configuration (pytestConfigFile.optional).
func pytestOptionalMarker(rel string) bool {
	candidate, found := pytestConfigFileAt(rel)
	return found && candidate.optional
}

// pytestConfiguration names the configuration pytest would use at the repository root: the file
// and, where the file needs one, the table or section that makes it pytest's, such as
// "pyproject.toml [tool.pytest.ini_options]". It reports false when no file pytest reads holds
// pytest configuration.
func pytestConfiguration(inputs verificationInputs) (string, bool, error) {
	for _, candidate := range pytestConfigFiles {
		data, ok := inputs.files[candidate.name]
		if !ok {
			continue
		}
		if len(candidate.tables) == 0 && len(candidate.sections) == 0 {
			return candidate.name, true, nil
		}
		header, found, err := pytestHeader(string(data), candidate)
		if err != nil {
			return "", false, err
		}
		if found {
			return candidate.name + " " + header, true, nil
		}
	}
	return "", false, nil
}

// pytestHeader reads candidate's TOML tables with the TOML parser and INI sections by their
// section syntax. The verification input byte bound caps both reads.
func pytestHeader(data string, candidate pytestConfigFile) (string, bool, error) {
	if len(candidate.tables) > 0 {
		var document map[string]any
		if err := util.DecodeTOML(candidate.name, []byte(data), &document); err != nil {
			return "", false, err
		}
		for _, name := range candidate.tables {
			if tomlHasTable(document, name) {
				return "[" + name + "]", true, nil
			}
		}
	}
	for _, line := range strings.Split(data, "\n") {
		if name, ok := iniSectionName(line); ok && slices.Contains(candidate.sections, name) {
			return "[" + name + "]", true, nil
		}
	}
	return "", false, nil
}

// tomlHasTable checks a dotted path in a parsed TOML document.
func tomlHasTable(document map[string]any, dotted string) bool {
	var current any = document
	segments := strings.Split(dotted, ".")
	if len(segments) > 16 {
		return false
	}
	for index := 0; index < len(segments) && index < 16; index++ {
		table, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = table[segments[index]]
	}
	table, ok := current.(map[string]any)
	return ok && tomlTableHasDirectValue(table)
}

// tomlTableHasDirectValue rejects a candidate reached only through a child subtable.
func tomlTableHasDirectValue(table map[string]any) bool {
	if len(table) == 0 {
		return true
	}
	keys := make([]string, 0, len(table))
	for key := range table {
		if len(keys) >= maxVerificationInputBytes {
			return false
		}
		keys = append(keys, key)
	}
	for index := 0; index < len(keys) && index < maxVerificationInputBytes; index++ {
		value := table[keys[index]]
		if _, nested := value.(map[string]any); !nested {
			return true
		}
	}
	return false
}

// iniSectionName returns the section a line of an INI file opens, as pytest's iniconfig parser
// reads it: the line starts with "[" in its first column (a line starting with whitespace
// continues a value), everything from the first "#" or ";" is a comment, and the rest, with
// trailing whitespace removed, must end with "]". The name between the brackets keeps its own
// spacing, so "[ pytest ]" names " pytest ", not pytest.
func iniSectionName(line string) (string, bool) {
	if !strings.HasPrefix(line, "[") {
		return "", false
	}
	if cut := strings.IndexAny(line, "#;"); cut >= 0 {
		line = line[:cut]
	}
	line = strings.TrimRight(line, " \t\r\f\v")
	if len(line) < 2 || !strings.HasSuffix(line, "]") {
		return "", false
	}
	return line[1 : len(line)-1], true
}
