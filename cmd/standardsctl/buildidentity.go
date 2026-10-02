// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import "github.com/cordanaLLM/praetor/internal/buildid"

// lockPin is the .standards.lock pinned_version init writes for a build identity, which
// internal/buildid resolves for every binary. A release is pinned verbatim, a revision as
// v0.0.0 with the revision (and "dirty") as SemVer build metadata. ok is false when the build
// identifies nothing and the pin is the zero version.
func lockPin(id buildid.Identity) (pinned string, ok bool) {
	switch {
	case id.Release != "":
		return id.Release, true
	case id.Revision != "":
		return formatDevLockVersion(id.Revision, id.Modified), true
	}
	return unidentifiedLockVersion, false
}
