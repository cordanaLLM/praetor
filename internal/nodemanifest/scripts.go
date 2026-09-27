// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import (
	"slices"
	"strings"
)

// npmInitTestScript is the test script `npm init` writes into a new package.json. It prints an
// error and exits 1, so a manifest still carrying it has no test that can pass. A repository
// that runs `npm init -y` to install one tool (commitlint, markdownlint) keeps it verbatim.
const npmInitTestScript = `echo "Error: no test specified" && exit 1`

// Manager is a Node package manager a package.json's packageManager field can name.
type Manager string

// The package managers a packageManager value can name.
const (
	ManagerNpm  Manager = "npm"
	ManagerPnpm Manager = "pnpm"
	ManagerYarn Manager = "yarn"
	ManagerBun  Manager = "bun"
)

// managerLockfiles lists every Manager with the root lockfiles its locked install reads,
// preferred first. npm reads package-lock.json only: npm 12 reads no npm-shrinkwrap.json. Bun
// writes bun.lock since 1.2 and still reads the binary bun.lockb it wrote before.
var managerLockfiles = []struct {
	manager   Manager
	lockfiles []string
}{
	{ManagerNpm, []string{"package-lock.json"}},
	{ManagerPnpm, []string{"pnpm-lock.yaml"}},
	{ManagerYarn, []string{"yarn.lock"}},
	{ManagerBun, []string{"bun.lock", "bun.lockb"}},
}

// Lockfiles returns the root lockfiles m's locked install reads, preferred first, or nil for a
// value that is not a known Manager.
func (m Manager) Lockfiles() []string {
	for _, entry := range managerLockfiles {
		if entry.manager == m {
			return slices.Clone(entry.lockfiles)
		}
	}
	return nil
}

// AllLockfiles returns every known Manager's root lockfiles, npm's first.
func AllLockfiles() []string {
	var all []string
	for _, entry := range managerLockfiles {
		all = append(all, entry.lockfiles...)
	}
	return all
}

// LockfileManager returns the Manager whose locked install reads the root lockfile name.
func LockfileManager(name string) (Manager, bool) {
	for _, entry := range managerLockfiles {
		if slices.Contains(entry.lockfiles, name) {
			return entry.manager, true
		}
	}
	return "", false
}

// DeclaredManager splits a package.json packageManager value, "<name>@<version>" as Corepack
// defines it, into the Manager it names and that version. An empty value names no manager and
// is valid: it returns "" and true. A value that is not of that form, or names a manager this
// package does not know, returns false. The value is matched exactly, so surrounding whitespace
// or a bare name is not a declaration.
func DeclaredManager(packageManager string) (manager Manager, version string, ok bool) {
	if packageManager == "" {
		return "", "", true
	}
	name, version, found := strings.Cut(packageManager, "@")
	if !found || Manager(name).Lockfiles() == nil {
		return "", "", false
	}
	return Manager(name), version, true
}

// NpmRuns reports whether npm is the package manager a package.json's packageManager value
// selects: npm itself ("npm@<version>"), or no value, since npm is what runs a manifest that
// names none. npm cannot stand in for another manager: it ignores that manager's lockfile and
// resolves the dependency graph afresh.
//
// Adoption's verification plan decides with this. The typescript-node CI scaffold reads the
// same field through DeclaredManager and installs with whichever manager it names.
func NpmRuns(packageManager string) bool {
	manager, _, ok := DeclaredManager(packageManager)
	return ok && (manager == "" || manager == ManagerNpm)
}

// ScriptRuns reports whether a package.json script is a command that can pass: neither empty
// nor blank, and not the test placeholder `npm init` writes, which always exits 1. Running a
// script that fails this is a gate no change can satisfy, not a passing one.
func ScriptRuns(script string) bool {
	trimmed := strings.TrimSpace(script)
	return trimmed != "" && trimmed != npmInitTestScript
}
