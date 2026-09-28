// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// errBatchShimArgument refuses an argument a batch-file shim cannot forward unchanged.
var errBatchShimArgument = errors.New("argument cannot pass through a batch-file shim unchanged")

// batchShimSafe lists every character batchShimArgument passes to a batch-file shim: letters,
// digits and the punctuation of an npm package name and a bare, caret or tilde SemVer range.
// cmd.exe gives none of them a meaning outside quotes except the caret, which is escaped.
const batchShimSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@/._-+~^"

// pnpmUpdateCommand returns the executable and arguments that run `pnpm update spec` on goos,
// where lookPath resolves pnpm as os/exec does.
//
// Outside Windows, and on Windows when pnpm is not a batch file, it is exactly
// ("pnpm", "update", spec). On Windows pnpm is usually the npm shim pnpm.cmd, and CreateProcess
// runs a batch file through cmd.exe, which parses the arguments with its own rules instead of
// the CommandLineToArgvW rules os/exec quotes for. Go escapes an argument only for a space, a
// tab or a quote, so a caret range such as lib@^5.7.3 reached cmd.exe bare and lost its caret,
// and pnpm moved the lockfile to an exact version while package.json kept the range (#508).
// For a batch file the resolved path is run and spec is escaped by batchShimArgument.
func pnpmUpdateCommand(goos string, lookPath func(string) (string, error), spec string) (string, []string, error) {
	if goos != "windows" {
		return "pnpm", []string{"update", spec}, nil
	}
	resolved, batch := resolvedBatchFile(lookPath, "pnpm")
	if !batch {
		return "pnpm", []string{"update", spec}, nil
	}
	escaped, err := batchShimArgument(spec)
	if err != nil {
		return "", nil, fmt.Errorf("pnpm resolves to the batch file %s: %w", resolved, err)
	}
	return resolved, []string{"update", escaped}, nil
}

// resolvedBatchFile resolves name with lookPath and reports whether it is a Windows batch
// file, which CreateProcess starts through cmd.exe: its extension is .bat or .cmd in any case.
// A name that does not resolve is not one, so it is run by name and the run reports why it
// could not start, as it always did.
func resolvedBatchFile(lookPath func(string) (string, error), name string) (string, bool) {
	resolved, err := lookPath(name)
	ext := filepath.Ext(resolved)
	return resolved, err == nil && (strings.EqualFold(ext, ".cmd") || strings.EqualFold(ext, ".bat"))
}

// batchShimArgument returns arg as it must be written for a batch-file shim to hand it
// unchanged to the program the shim forwards %* to.
//
// cmd.exe reads the unquoted caret as its escape character, removing it and keeping the
// character after it. The argument is read twice: once when cmd.exe parses the command line
// that runs the batch file, and again when the shim's own line expands %* and is parsed. Each
// read halves a run of carets, so one caret is written as four. This is the double escaping
// cross-spawn applies to npm cmd shims.
//
// Every other character that means something to cmd.exe (%, !, a delimiter such as a space,
// a comma or an equals sign, a quote, a redirection or grouping character) is refused rather
// than escaped: an npm package name and a bare, caret or tilde range contain none, and a
// value that does is not one pnpm update should be handed through a shell.
func batchShimArgument(arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("%w: empty argument", errBatchShimArgument)
	}
	if index := strings.IndexFunc(arg, func(r rune) bool { return !strings.ContainsRune(batchShimSafe, r) }); index >= 0 {
		refused, _ := utf8.DecodeRuneInString(arg[index:])
		return "", fmt.Errorf("%w: %q holds %q, which cmd.exe would reinterpret", errBatchShimArgument, arg, refused)
	}
	return strings.ReplaceAll(arg, "^", "^^^^"), nil
}
