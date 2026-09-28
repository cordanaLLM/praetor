// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"path/filepath"
	"strings"
)

// sourceLanguages maps a lower-cased file extension to the programming language whose source
// the file holds.
var sourceLanguages = map[string]string{
	".go": "go", ".rs": "rust", ".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp",
	".py": "python", ".ts": "typescript", ".tsx": "typescript",
	".js": "javascript", ".jsx": "javascript", ".svelte": "svelte",
	".sh": "shell", ".bash": "shell",
}

// SourceLanguage returns the programming language a file's extension marks, such as "go",
// "rust" or "typescript", or "" for a file that is not program source (configuration,
// documentation, data). The match ignores case and reads the extension only.
//
// It is the one extension table the editor capability probe and the dedupe scope share: the
// probe configures tooling per language it observes, and the dedupe scan names the languages
// it found but cannot read, so the two cannot disagree about what a file is.
func SourceLanguage(name string) string {
	return sourceLanguages[strings.ToLower(filepath.Ext(name))]
}
