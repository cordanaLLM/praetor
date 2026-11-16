// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path"
	"slices"
	"strings"
)

// CommandName reduces a shell program path to its basename without a Windows .exe suffix.
func CommandName(word string) string {
	return strings.TrimSuffix(path.Base(strings.ReplaceAll(word, `\`, "/")), ".exe")
}

// CommandNames returns an independent list normalized with CommandName.
func CommandNames(names []string) []string {
	normalized := slices.Clone(names)
	for i, name := range normalized {
		normalized[i] = CommandName(name)
	}
	return normalized
}
