// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// windowsExecutableExts are the extensions a shell-less spawn starts on Windows: CreateProcess
// runs .com and .exe images, and Node's process_wrap rejects .bat and .cmd with EINVAL, so a
// batch-file language server never starts from the VS Code client.
var windowsExecutableExts = map[string]bool{".exe": true, ".com": true}

// ExecutableFile reports whether info, the file information of path, is a regular file goos can
// execute. Windows carries executability in the file extension rather than in a permission bit,
// and Go reports 0666 or 0444 for every file there, so the Unix bit test alone would answer "not
// executable" for every Windows file (HISS-21). The caller chooses os.Stat or os.Lstat: the
// editor capability probe refuses a link, the secret scanner follows one to a tool's real binary.
func ExecutableFile(path string, info fs.FileInfo, goos string) bool {
	if info == nil || !info.Mode().IsRegular() {
		return false
	}
	if goos == "windows" {
		return windowsExecutableExts[strings.ToLower(filepath.Ext(path))]
	}
	return info.Mode().Perm()&0o111 != 0
}
