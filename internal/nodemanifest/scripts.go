// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import "strings"

// npmInitTestScript is the test script `npm init` writes into a new package.json. It prints an
// error and exits 1, so a manifest still carrying it has no test that can pass. A repository
// that runs `npm init -y` to install one tool (commitlint, markdownlint) keeps it verbatim.
const npmInitTestScript = `echo "Error: no test specified" && exit 1`

// NpmRuns reports whether npm is the package manager a package.json's packageManager value
// selects: npm itself ("npm@<version>"), or no value, since npm is what runs a manifest that
// names none. npm cannot stand in for another manager: it ignores that manager's lockfile and
// resolves the dependency graph afresh.
//
// Adoption's verification plan and the typescript-node CI scaffold both decide with this, so
// the Makefile adoption writes and the CI job flavor apply writes agree on who installs.
func NpmRuns(packageManager string) bool {
	return packageManager == "" || strings.HasPrefix(packageManager, "npm@")
}

// ScriptRuns reports whether a package.json script is a command that can pass: neither empty
// nor blank, and not the test placeholder `npm init` writes, which always exits 1. Running a
// script that fails this is a gate no change can satisfy, not a passing one.
func ScriptRuns(script string) bool {
	trimmed := strings.TrimSpace(script)
	return trimmed != "" && trimmed != npmInitTestScript
}
