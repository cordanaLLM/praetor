// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// frameworkLanguages are the languages a framework target can be configured for
// (framework.targets.<language>). They are the languages the needs analyzers report, so the
// schema and the analyzers read one list.
var frameworkLanguages = [...]string{"go", "typescript", "python", "rust", "native"}

// FrameworkLanguages returns the languages framework.targets accepts, in analyzer order.
func FrameworkLanguages() []string {
	return slices.Clone(frameworkLanguages[:])
}

// ParseFrameworkLanguage returns value when it names a framework language, and an error
// listing the accepted languages otherwise.
func ParseFrameworkLanguage(value string) (string, error) {
	if slices.Contains(frameworkLanguages[:], value) {
		return value, nil
	}
	return "", fmt.Errorf("unknown framework language %q (want one of %s)", value, strings.Join(frameworkLanguages[:], ", "))
}

// IsModulePathShaped reports whether value looks like a Go module path rather than a
// filesystem location: it must not be absolute or relative-prefixed, it must have more
// than one element, and its first element must be a host, that is contain a dot. The
// operator schema checks framework.targets.<language>.module with it, and the needs engine
// uses it to tell a module path from a checkout path.
func IsModulePathShaped(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "~") {
		return false
	}
	first, _, ok := strings.Cut(value, "/")
	return ok && strings.Contains(first, ".")
}
