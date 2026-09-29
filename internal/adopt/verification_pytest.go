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
	{name: "pyproject.toml", tables: []string{"tool.pytest", "tool.pytest.ini_options"}},
	{name: "tox.ini", sections: []string{"pytest"}},
	{name: "setup.cfg", sections: []string{"tool:pytest"}},
}

// pytestConfigMarker reports a root file pytestConfigFiles names, which the verification walk
// records for pytestConfiguration.
func pytestConfigMarker(rel string) bool {
	for _, candidate := range pytestConfigFiles {
		if candidate.name == rel {
			return true
		}
	}
	return false
}

// pytestPresenceMarker reports a pytest configuration file whose presence alone makes it pytest's
// configuration, so the walk records it without reading it.
func pytestPresenceMarker(rel string) bool {
	for _, candidate := range pytestConfigFiles {
		if candidate.name == rel {
			return len(candidate.tables) == 0 && len(candidate.sections) == 0
		}
	}
	return false
}

// pytestConfiguration names the configuration pytest would use at the repository root: the file
// and, where the file needs one, the table or section that makes it pytest's, such as
// "pyproject.toml [tool.pytest.ini_options]". It reports false when no file pytest reads holds
// pytest configuration.
func pytestConfiguration(inputs verificationInputs) (string, bool) {
	for _, candidate := range pytestConfigFiles {
		data, ok := inputs.files[candidate.name]
		if !ok {
			continue
		}
		if len(candidate.tables) == 0 && len(candidate.sections) == 0 {
			return candidate.name, true
		}
		if header, found := pytestHeader(string(data), candidate); found {
			return candidate.name + " " + header, true
		}
	}
	return "", false
}

// pytestHeader returns the first header in data that makes it pytest configuration: one of
// candidate's TOML tables, read by util.TOMLTableName, or one of its INI sections, read as the
// iniconfig parser pytest uses reads a section line (iniSectionName). Like util.TOMLTableName it
// reads each line's shape and is not a parser: a table written only as dotted keys under [tool]
// is not seen, and a header-shaped line inside a multi-line string is. The metadata byte bound
// caps data, and so the number of lines read.
func pytestHeader(data string, candidate pytestConfigFile) (string, bool) {
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if name, ok := tomlTableHeader(line); ok && slices.Contains(candidate.tables, name) {
			return "[" + name + "]", true
		}
		if name, ok := iniSectionName(line); ok && slices.Contains(candidate.sections, name) {
			return "[" + name + "]", true
		}
	}
	return "", false
}

// tomlTableHeader returns the name of the standard table line opens; an array-of-tables header
// ([[name]]) and any other line open none.
func tomlTableHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false
	}
	return util.TOMLTableName(trimmed), true
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
