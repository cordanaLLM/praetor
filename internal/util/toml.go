// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// TOMLTableName normalizes a TOML table header line to its name with the spaces TOML permits
// removed and a trailing comment dropped: "[ extend ]" is "extend" and "[[ rules ]]  # x" is
// "[rules]", an array-of-tables header keeping one bracket pair. It reads a line's shape and is
// not a parser: the module carries no TOML library, and the callers (the gitleaks configuration
// check in internal/flavor and the REUSE.toml read of praetorctl audit) need only the header.
func TOMLTableName(header string) string {
	name, _, _ := strings.Cut(header, "#")
	name = strings.ReplaceAll(strings.TrimSpace(name), " ", "")
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	return strings.Trim(name, `"'`)
}
