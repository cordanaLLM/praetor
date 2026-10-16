// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gomanifest

import "strings"

// ToolModuleFile is the repository-relative module Praetor recognises as the version source for
// Go command-line tools. Adoption's hooks and flavor workflows make the same decision from it.
const ToolModuleFile = "tools/go/go.mod"

// DeclaresTool reports whether manifest pins modulePath as a requirement or names packagePath
// (or the module path retained for compatibility with earlier adoption output) in a tool
// directive. The go command needs the requirement to select a version; Go 1.24 and later can
// additionally name the runnable package with tool.
func DeclaresTool(manifest []byte, modulePath, packagePath string) bool {
	parsed := ParseManifest(manifest)
	for i := 0; i < len(parsed.Requires) && i < maxManifestLines; i++ {
		if parsed.Requires[i].Path == modulePath {
			return true
		}
	}
	lines := strings.Split(string(TrimBOM(manifest)), "\n")
	inTool := false
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		line, isTool := ToolLine(lines[i], &inTool)
		fields := strings.Fields(line)
		if isTool && len(fields) > 0 && (fields[0] == packagePath || fields[0] == modulePath) {
			return true
		}
	}
	return false
}
