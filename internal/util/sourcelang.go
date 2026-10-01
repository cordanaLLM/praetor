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
//
// An extension two common languages share is left out rather than guessed: .m (Objective-C
// or MATLAB), .fs (F# or a GLSL fragment shader), .v (Verilog or V). A missing language only
// costs the dedupe scan a Not Scanned line; a wrong one names a language the repository does
// not have.
//
// The POSIX shell family (.sh, plus .bash for Bash) is "shell", the language the HISS shell
// scanner reads. Zsh and the Korn shell keep their own names, since their syntax goes beyond
// POSIX and no HISS scanner reads them: a coverage report then lists them as unscanned instead
// of under the shell it verified.
var sourceLanguages = map[string]string{
	".go": "go", ".rs": "rust", ".zig": "zig", ".nim": "nim",
	".c": "c", ".h": "c",
	".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".hh": "cpp", ".hxx": "cpp",
	".cu": "cuda", ".cuh": "cuda",
	".py": "python",
	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
	".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
	".svelte": "svelte", ".vue": "vue",
	".java": "java", ".kt": "kotlin", ".kts": "kotlin", ".scala": "scala", ".groovy": "groovy",
	".clj": "clojure", ".cljs": "clojure", ".cljc": "clojure",
	".cs": "csharp", ".swift": "swift", ".dart": "dart",
	".rb": "ruby", ".php": "php", ".lua": "lua", ".pl": "perl", ".pm": "perl",
	".r": "r", ".jl": "julia",
	".ex": "elixir", ".exs": "elixir", ".erl": "erlang", ".hrl": "erlang",
	".hs": "haskell", ".ml": "ocaml", ".mli": "ocaml",
	".sh": "shell", ".bash": "shell", ".zsh": "zsh", ".ksh": "ksh",
	".ps1": "powershell", ".psm1": "powershell",
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
