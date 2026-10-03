// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package workstation implements `praetorctl workstation install` and `status`: building
// this engine's binaries (binaryNames) from a configured checkout and placing them atomically
// in a bin directory, and reporting what is installed there.
//
// It is the Go port of scripts/dev_install.py's install logic (HISS-19: dev_install.py now
// calls this package through the CLI instead of reimplementing it) plus a writer for the
// manifest S1's reader (internal/config/install_manifest.go) already validates.
//
// update, rollback and schedule rendering are later packages (W2, W3); this package's
// exported surface -- BuildFunc, Options.GOOS, and the target-name tables below -- is the
// seam they are expected to build on rather than duplicate.
package workstation

import (
	"errors"

	"github.com/cordanaLLM/praetor/internal/config"
)

// binaryNames are the installed binaries, in a stable build and manifest order: the three
// engine binaries, then tribunusctl, the Tribunus model data sync docs/tribunus/data-sync.md
// documents as a bare command (#377). It is the list the install manifest validates against
// (config.InstalledBinaryNames), so the writer and the reader cannot disagree; every table
// below is keyed on it.
var binaryNames = config.InstalledBinaryNames()

// buildPackages is the module path `go build` reads each binary from.
var buildPackages = map[string]string{
	"praetorctl":  "./cmd/standardsctl",
	"praetor-mcp": "./cmd/standards-mcp",
	"praetor-lsp": "./cmd/standards-lsp",
	"tribunusctl": "./tribunus/cmd/tribunusctl",
}

// binaryAliases is the legacy name kept as a symlink beside each binary, matching the
// aliases scripts/dev_install.py installed before this package existed. tribunusctl never
// had another name, so it has no entry and no alias is placed for it.
var binaryAliases = map[string]string{
	"praetorctl":  "standardsctl",
	"praetor-mcp": "standards-mcp",
	"praetor-lsp": "standards-lsp",
}

var (
	// ErrLockHeld reports that another install or update holds the exclusive bin-dir lock.
	ErrLockHeld = errors.New("workstation: an installation lock is already held")
	// ErrForeignTarget reports an installation target this package did not create: a
	// symlink pointing somewhere unexpected, or a non-regular file (directory, device,
	// pipe, socket). Installing over either could destroy operator data or follow an
	// attacker's link, so both are refused before anything is touched.
	ErrForeignTarget = errors.New("workstation: refusing an unexpected installation target")
)

// targetsOf returns the installation targets one binary owns: its own name, then its legacy
// alias when it has one.
func targetsOf(name string) []string {
	if alias, ok := binaryAliases[name]; ok {
		return []string{name, alias}
	}
	return []string{name}
}

// allTargetNames returns the binaries followed by their aliases: the fixed order every
// target-name loop in this package uses, so a partial-failure log always names targets in
// the same sequence a re-run would touch them in.
func allTargetNames() []string {
	names := make([]string, 0, 2*len(binaryNames))
	names = append(names, binaryNames...)
	for _, name := range binaryNames {
		names = append(names, targetsOf(name)[1:]...)
	}
	return names
}

// primaryFor returns the primary binary name an alias stands in for, and ok=false for any
// other name (including a primary binary's own name).
func primaryFor(alias string) (string, bool) {
	for _, name := range binaryNames {
		if candidate, ok := binaryAliases[name]; ok && candidate == alias {
			return name, true
		}
	}
	return "", false
}

// missingBinaries returns the binaries manifest does not record, in build order. An install
// made before a binary joined binaryNames lacks it: status reports that install as not
// current, and a refresh reinstalls it at the same commit, until an install records it.
func missingBinaries(manifest config.InstallManifest) []string {
	var missing []string
	for _, name := range binaryNames {
		if _, ok := manifest.Binaries[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}
