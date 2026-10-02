// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dogfood

import (
	"maps"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// Positive: a stamped build records its full revision, the dirty flag and the toolchain.
func TestEngineBuild_Positive_RecordsTheStamp(t *testing.T) {
	revision := strings.Repeat("b", 40)
	info := &debug.BuildInfo{GoVersion: "go1.27", Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "true"},
	}}
	want := map[string]string{"revision": revision, "modified": "true", "go_version": "go1.27"}
	if got := engineBuild(info); !maps.Equal(got, want) {
		t.Fatalf("engineBuild = %v, want %v", got, want)
	}
}

// Negative: without build information, or without a VCS stamp, the revision is unavailable
// and no dirty flag is claimed.
func TestEngineBuild_Negative_Unstamped(t *testing.T) {
	if got := engineBuild(nil); !maps.Equal(got, map[string]string{"revision": "unavailable"}) {
		t.Fatalf("nil info: %v", got)
	}
	got := engineBuild(&debug.BuildInfo{GoVersion: "go1.27"})
	if want := map[string]string{"revision": "unavailable", "go_version": "go1.27"}; !maps.Equal(got, want) {
		t.Fatalf("unstamped info: %v, want %v", got, want)
	}
}

// Boundary: the report reads the stamp exactly as buildid.Stamp does. Its own scan used to
// stop after 64 settings, so a stamp further down was "unavailable" here while the version
// command and the engine-build check read it (#666).
func TestEngineBuild_Boundary_SameScanAsTheSharedReader(t *testing.T) {
	settings := make([]debug.BuildSetting, 100)
	settings[99] = debug.BuildSetting{Key: "vcs.revision", Value: strings.Repeat("c", 40)}
	info := &debug.BuildInfo{Settings: settings}
	revision, _ := buildid.Stamp(info)
	if got := engineBuild(info)["revision"]; got != revision || got == "unavailable" {
		t.Fatalf("engine_build revision %q, shared reader %q", got, revision)
	}
}
