// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// interpolationMarkers are the spellings a client, shell or template engine could expand.
// A literal value never carries one, so a configuration file cannot smuggle an environment
// read or a file inclusion into a value the engine hands on verbatim.
var interpolationMarkers = [...]string{"$", "`", "{env:", "{file:"}

// LiteralString reports whether value is a bounded literal: at most maxBytes, valid UTF-8,
// no control byte and no interpolation marker. The MCP registry, connection profiles and the
// operator settings sections all apply this one rule.
func LiteralString(value string, maxBytes int) bool {
	if len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, marker := range interpolationMarkers {
		if strings.Contains(value, marker) {
			return false
		}
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// CleanAbsoluteLiteral reports whether value is a literal, absolute, already clean host path
// other than the filesystem root.
func CleanAbsoluteLiteral(value string, maxBytes int) bool {
	return LiteralString(value, maxBytes) && filepath.IsAbs(value) && filepath.Clean(value) == value &&
		value != string(filepath.Separator)
}
