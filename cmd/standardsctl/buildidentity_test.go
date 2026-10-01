// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"runtime/debug"
	"testing"
)

// fullRevision is a 40-hex commit as Go's VCS stamp records it; its first 12 characters are
// what output shows.
const fullRevision = "25451d888c8710822dd578907625dc69a0975142"

// moduleBuild is the build information of a `go install module@version` build: a module
// version and no vcs.* settings, because the module cache carries no VCS metadata.
func moduleBuild(moduleVersion string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{
		Path:    "example.com/tool",
		Version: moduleVersion,
	}}
}

// stampedBuild is the build information of a checkout build Go stamped with its commit.
func stampedBuild(moduleVersion string, modified bool) *debug.BuildInfo {
	info := moduleBuild(moduleVersion)
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

type identityCase struct {
	name       string
	injected   string
	info       *debug.BuildInfo
	display    string
	pinned     string
	identified bool
}

func checkIdentity(t *testing.T, tc identityCase) {
	t.Helper()
	id := identifyBuild(tc.injected, tc.info)
	if got := id.display(); got != tc.display {
		t.Errorf("%s: display = %q, want %q", tc.name, got, tc.display)
	}
	pinned, identified := id.lockPin()
	if pinned != tc.pinned || identified != tc.identified {
		t.Errorf("%s: lockPin = %q, %v; want %q, %v", tc.name, pinned, identified, tc.pinned, tc.identified)
	}
	if !lockPattern.MatchString(pinned) {
		t.Errorf("%s: pinned_version %q does not satisfy the lock validator", tc.name, pinned)
	}
}

// Positive: a `go install module@<commit>` build carries no VCS stamp, but its pseudo-version
// names the commit. It is reported and pinned as the checkout build of that commit is (#642).
func TestIdentifyBuildReadsThePseudoVersionOfAModuleInstall(t *testing.T) {
	for _, tc := range []identityCase{
		{name: "untagged base", info: moduleBuild("v0.0.0-20260929221210-25451d888c87")},
		{name: "after a release", info: moduleBuild("v1.2.4-0.20260929221210-25451d888c87")},
		{name: "after a prerelease", info: moduleBuild("v1.2.3-rc.1.0.20260929221210-25451d888c87")},
		{name: "with build metadata", info: moduleBuild("v2.0.1-0.20260929221210-25451d888c87+incompatible")},
	} {
		tc.display, tc.pinned, tc.identified = "25451d888c87", "v0.0.0+25451d888c87", true
		checkIdentity(t, tc)
	}
}

// Positive: a `go install module@vX.Y.Z` build records the tag, which is a release version and
// is reported and pinned verbatim, as an injected release is.
func TestIdentifyBuildReportsATaggedModuleVersionAsARelease(t *testing.T) {
	for _, tag := range []string{"v1.4.2", "v2.0.0-rc.1"} {
		checkIdentity(t, identityCase{name: tag, info: moduleBuild(tag), display: tag, pinned: tag, identified: true})
	}
}

// Positive: the stronger sources keep their precedence. An injected release beats everything,
// and a VCS stamp, which also carries the dirty flag, beats the module version Go derives
// from it.
func TestIdentifyBuildPrefersInjectedVersionThenVCSStamp(t *testing.T) {
	pseudo := "v0.0.0-20260101000000-0123456789ab"
	for _, tc := range []identityCase{
		{name: "injected", injected: "v1.4.2", info: stampedBuild(pseudo, true),
			display: "v1.4.2", pinned: "v1.4.2", identified: true},
		{name: "clean stamp", info: stampedBuild(pseudo, false),
			display: "25451d888c87", pinned: "v0.0.0+25451d888c87", identified: true},
		{name: "dirty stamp", info: stampedBuild(pseudo+"+dirty", true),
			display: "25451d888c87-dirty", pinned: "v0.0.0+25451d888c87.dirty", identified: true},
	} {
		checkIdentity(t, tc)
	}
}

// Negative: a build without an injected version, a VCS stamp or a usable module version
// cannot identify itself. It must say so and pin the zero version, never a version it cannot
// stand behind.
func TestIdentifyBuildRefusesAnUnidentifiableBuild(t *testing.T) {
	const untagged = "unknown (untagged build, no VCS stamp)"
	for _, tc := range []identityCase{
		{name: "no build information", info: nil, display: "unknown (no build information)"},
		{name: "devel", info: moduleBuild("(devel)"), display: untagged},
		{name: "empty", info: moduleBuild(""), display: untagged},
		{name: "blank injection", injected: "   ", info: moduleBuild("(devel)"), display: untagged},
		{name: "not a version", info: moduleBuild("latest"), display: untagged},
		{name: "leading zero", info: moduleBuild("v01.0.0-20260929221210-25451d888c87"), display: untagged},
	} {
		tc.pinned, tc.identified = unidentifiedLockVersion, false
		checkIdentity(t, tc)
	}
}

// Boundary: only Go's pseudo-version grammar yields a revision. A 12-character revision is
// kept, a longer one is shortened as a VCS stamp is, a shorter one is kept whole, and a
// timestamp that is not exactly 14 digits is no pseudo-version.
func TestPseudoVersionRevisionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		version  string
		revision string
		ok       bool
	}{
		{"v0.0.0-20260929221210-25451d888c87", "25451d888c87", true},
		{"v0.0.0-20260929221210-" + fullRevision, fullRevision, true},
		{"v0.0.0-20260929221210-abc1234", "abc1234", true},
		{"v0.0.0-2026092922121-25451d888c87", "", false},
		{"v0.0.0-202609292212100-25451d888c87", "", false},
		{"v1.2.3-20260929221210-25451d888c87", "", false},
		{"v1.4.2", "", false},
		{"", "", false},
	} {
		revision, ok := pseudoVersionRevision(tc.version)
		if revision != tc.revision || ok != tc.ok {
			t.Errorf("pseudoVersionRevision(%q) = %q, %v; want %q, %v", tc.version, revision, ok, tc.revision, tc.ok)
		}
	}
	long := identifyBuild("", moduleBuild("  v0.0.0-20260929221210-"+fullRevision+"  "))
	if got := long.display(); got != "25451d888c87" {
		t.Errorf("a padded pseudo-version with a full revision must report 12 characters, got %q", got)
	}
}
