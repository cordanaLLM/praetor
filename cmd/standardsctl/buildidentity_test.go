// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"runtime/debug"
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// fullRevision is a 40-hex commit as Go's VCS stamp records it; its first 12 characters are
// what output shows.
const fullRevision = "25451d888c8710822dd578907625dc69a0975142"

// moduleBuild is the build information of a `go install module@version` build: a module
// version and no vcs.* settings, because the module cache carries no VCS metadata.
func moduleBuild(moduleVersion string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "example.com/tool", Version: moduleVersion}}
}

// stampedBuild is the build information of a checkout build Go stamped with its commit.
func stampedBuild(modified bool) *debug.BuildInfo {
	info := moduleBuild("v0.0.0-20260101000000-0123456789ab")
	dirty := "false"
	if modified {
		dirty = "true"
	}
	info.Settings = []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: fullRevision},
		{Key: "vcs.modified", Value: dirty},
	}
	return info
}

type pinCase struct {
	name       string
	injected   string
	info       *debug.BuildInfo
	pinned     string
	identified bool
}

func checkPin(t *testing.T, tc pinCase) {
	t.Helper()
	pinned, identified := lockPin(buildid.Identify(tc.injected, tc.info))
	if pinned != tc.pinned || identified != tc.identified {
		t.Errorf("%s: lockPin = %q, %v; want %q, %v", tc.name, pinned, identified, tc.pinned, tc.identified)
	}
	if !lockPattern.MatchString(pinned) {
		t.Errorf("%s: pinned_version %q does not satisfy the lock validator", tc.name, pinned)
	}
}

// Positive: a release, injected or a `go install` of a tag, is pinned verbatim; a revision,
// from a VCS stamp or the pseudo-version of a `go install module@<commit>` build, is pinned
// as v0.0.0 with the revision as build metadata, so one commit pins the same however it was
// built (#642).
func TestLockPin_Positive_FollowsTheSharedIdentity(t *testing.T) {
	for _, tc := range []pinCase{
		{name: "injected", injected: "v1.4.2", info: stampedBuild(true), pinned: "v1.4.2", identified: true},
		{name: "tag", info: moduleBuild("v2.0.0-rc.1"), pinned: "v2.0.0-rc.1", identified: true},
		{name: "clean stamp", info: stampedBuild(false), pinned: "v0.0.0+25451d888c87", identified: true},
		{name: "dirty stamp", info: stampedBuild(true), pinned: "v0.0.0+25451d888c87.dirty", identified: true},
		{name: "pseudo-version", info: moduleBuild("v1.2.4-0.20260929221210-25451d888c87"),
			pinned: "v0.0.0+25451d888c87", identified: true},
	} {
		checkPin(t, tc)
	}
}

// Negative: a build that identifies nothing pins the zero version and says so, never a
// version it cannot stand behind.
func TestLockPin_Negative_UnidentifiableBuild(t *testing.T) {
	for _, tc := range []pinCase{
		{name: "no build information", info: nil},
		{name: "devel", info: moduleBuild("(devel)")},
		{name: "blank injection", injected: "   ", info: moduleBuild("")},
		{name: "not a version", info: moduleBuild("latest")},
	} {
		tc.pinned, tc.identified = unidentifiedLockVersion, false
		checkPin(t, tc)
	}
}

// Boundary: a pseudo-version carrying a full 40-character revision pins the same 12
// characters a VCS stamp of that commit pins.
func TestLockPin_Boundary_FullRevisionPseudoVersion(t *testing.T) {
	checkPin(t, pinCase{name: "full revision", info: moduleBuild("v0.0.0-20260929221210-" + fullRevision),
		pinned: "v0.0.0+25451d888c87", identified: true})
}
