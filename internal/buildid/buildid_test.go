// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package buildid

import (
	"runtime/debug"
	"strings"
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
	name     string
	injected string
	info     *debug.BuildInfo
	want     Identity
}

func checkIdentity(t *testing.T, tc identityCase, display string) {
	t.Helper()
	got := Identify(tc.injected, tc.info)
	if got != tc.want {
		t.Errorf("%s: Identify = %+v, want %+v", tc.name, got, tc.want)
	}
	if got.String() != display {
		t.Errorf("%s: String = %q, want %q", tc.name, got.String(), display)
	}
}

// Positive: a `go install module@<commit>` build carries no VCS stamp, but its pseudo-version
// names the commit. It is reported as the checkout build of that commit is (#642).
func TestIdentify_Positive_PseudoVersionOfAModuleInstall(t *testing.T) {
	for _, tc := range []identityCase{
		{name: "untagged base", info: moduleBuild("v0.0.0-20260929221210-25451d888c87")},
		{name: "after a release", info: moduleBuild("v1.2.4-0.20260929221210-25451d888c87")},
		{name: "after a prerelease", info: moduleBuild("v1.2.3-rc.1.0.20260929221210-25451d888c87")},
		{name: "with build metadata", info: moduleBuild("v2.0.1-0.20260929221210-25451d888c87+incompatible")},
	} {
		tc.want = Identity{Revision: "25451d888c87"}
		checkIdentity(t, tc, "25451d888c87")
	}
}

// Positive: a `go install module@vX.Y.Z` build records the tag, which is a release version and
// is reported verbatim, as an injected release is.
func TestIdentify_Positive_TaggedModuleVersionIsARelease(t *testing.T) {
	for _, tag := range []string{"v1.4.2", "v2.0.0-rc.1"} {
		checkIdentity(t, identityCase{name: tag, info: moduleBuild(tag), want: Identity{Release: tag}}, tag)
	}
}

// Positive: the stronger sources keep their precedence. An injected release beats everything,
// and a VCS stamp, which also carries the dirty flag, beats the module version Go derives
// from it.
func TestIdentify_Positive_InjectedVersionThenVCSStamp(t *testing.T) {
	pseudo := "v0.0.0-20260101000000-0123456789ab"
	checkIdentity(t, identityCase{name: "injected", injected: " v1.4.2 ", info: stampedBuild(pseudo, true),
		want: Identity{Release: "v1.4.2"}}, "v1.4.2")
	checkIdentity(t, identityCase{name: "development injection", injected: "dev-0123abcd", info: nil,
		want: Identity{Release: "dev-0123abcd"}}, "dev-0123abcd")
	checkIdentity(t, identityCase{name: "clean stamp", info: stampedBuild(pseudo, false),
		want: Identity{Revision: "25451d888c87"}}, "25451d888c87")
	checkIdentity(t, identityCase{name: "dirty stamp", info: stampedBuild(pseudo+"+dirty", true),
		want: Identity{Revision: "25451d888c87", Modified: true}}, "25451d888c87-dirty")
}

// Negative: a build without an injected version, a VCS stamp or a usable module version
// cannot identify itself. It must say so, never report a version it cannot stand behind.
func TestIdentify_Negative_UnidentifiableBuild(t *testing.T) {
	const untagged = "untagged build, no VCS stamp"
	blankStamp := moduleBuild("(devel)")
	blankStamp.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: "   "}}
	for _, tc := range []identityCase{
		{name: "no build information", info: nil, want: Identity{Unknown: "no build information"}},
		{name: "devel", info: moduleBuild("(devel)"), want: Identity{Unknown: untagged}},
		{name: "empty", info: moduleBuild(""), want: Identity{Unknown: untagged}},
		{name: "blank injection", injected: "   ", info: moduleBuild("(devel)"), want: Identity{Unknown: untagged}},
		{name: "blank stamp", info: blankStamp, want: Identity{Unknown: untagged}},
		{name: "not a version", info: moduleBuild("latest"), want: Identity{Unknown: untagged}},
		{name: "leading zero", info: moduleBuild("v01.0.0-20260929221210-25451d888c87"), want: Identity{Unknown: untagged}},
	} {
		checkIdentity(t, tc, "unknown ("+tc.want.Unknown+")")
	}
	if got := Identify("", nil).String(); got == "v1.0.0" {
		t.Errorf("an unidentified build must not report the old literal, got %q", got)
	}
}

// Boundary: only Go's pseudo-version grammar yields a revision. A 12-character revision is
// kept, a longer one is shortened as a VCS stamp is, a shorter one is kept whole, and a
// timestamp that is not exactly 14 digits is no pseudo-version.
func TestPseudoVersionRevision_Boundary(t *testing.T) {
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
	long := Identify("", moduleBuild("  v0.0.0-20260929221210-"+fullRevision+"  "))
	if got := long.String(); got != "25451d888c87" {
		t.Errorf("a padded pseudo-version with a full revision must report 12 characters, got %q", got)
	}
}

// Positive: the stamp reader returns the full revision and the dirty flag.
func TestStamp_Positive(t *testing.T) {
	if got, modified := Stamp(stampedBuild("", true)); got != fullRevision || !modified {
		t.Fatalf("got %q modified=%v", got, modified)
	}
}

// Negative: no build information, or none carrying a VCS stamp, stamps nothing.
func TestStamp_Negative_Unstamped(t *testing.T) {
	if got, modified := Stamp(nil); got != "" || modified {
		t.Fatalf("nil info: %q %v", got, modified)
	}
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "-trimpath", Value: "true"}}}
	if got, modified := Stamp(info); got != "" || modified {
		t.Fatalf("unstamped info: %q %v", got, modified)
	}
}

// Boundary: the scan stops at maxBuildSettings, so a stamp past it is not read.
func TestStamp_Boundary_ScanBound(t *testing.T) {
	settings := make([]debug.BuildSetting, maxBuildSettings, maxBuildSettings+1)
	settings[maxBuildSettings-1] = debug.BuildSetting{Key: "vcs.revision", Value: "last"}
	info := &debug.BuildInfo{Settings: settings}
	if got, _ := Stamp(info); got != "last" {
		t.Fatalf("stamp on the last scanned setting: %q", got)
	}
	info.Settings = append(settings[:maxBuildSettings-1], debug.BuildSetting{}, debug.BuildSetting{Key: "vcs.revision", Value: "beyond"})
	if got, _ := Stamp(info); got != "" {
		t.Fatalf("stamp past the bound: %q", got)
	}
}

// Boundary: Short keeps exactly ShortRevisionLen characters, keeps a shorter commit whole and
// trims surrounding whitespace first.
func TestShort_Boundary(t *testing.T) {
	exact := fullRevision[:ShortRevisionLen]
	for _, tc := range []struct{ in, want string }{
		{fullRevision, exact},
		{exact + "a", exact},
		{exact, exact},
		{exact[:ShortRevisionLen-1], exact[:ShortRevisionLen-1]},
		{"  " + fullRevision + "\n", exact},
		{"   ", ""},
		{"", ""},
	} {
		if got := Short(tc.in); got != tc.want {
			t.Errorf("Short(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Positive and negative: a stamp with a revision is that revision, -dirty when modified; a
// stamp without one proves nothing, even with the dirty flag set.
func TestFromStamp(t *testing.T) {
	if got := FromStamp(fullRevision, true).String(); got != "25451d888c87-dirty" {
		t.Errorf("dirty stamp = %q", got)
	}
	if got := FromStamp(fullRevision, false).String(); got != "25451d888c87" {
		t.Errorf("clean stamp = %q", got)
	}
	if got := FromStamp("", true); got.Revision != "" || got.Modified || !strings.HasPrefix(got.String(), "unknown (") {
		t.Errorf("empty stamp must be unknown, got %+v", got)
	}
}

// Positive and boundary: Running resolves the injected version against the running binary's
// own build information, and RunningInfo is that information.
func TestRunning(t *testing.T) {
	if got := Running("v9.8.7"); got != (Identity{Release: "v9.8.7"}) {
		t.Errorf("an injected release must win, got %+v", got)
	}
	if got, want := Running(""), Identify("", RunningInfo()); got != want {
		t.Errorf("Running(\"\") = %+v, Identify of the running build = %+v", got, want)
	}
	if RunningInfo() == nil {
		t.Error("a test binary is built with module support and must carry build information")
	}
}
