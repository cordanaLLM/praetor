// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dogfood

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// stampedInfo is the build information of a checkout build of revision.
func stampedInfo(revision string, modified bool) *debug.BuildInfo {
	dirty := "false"
	if modified {
		dirty = "true"
	}
	return &debug.BuildInfo{GoVersion: "go1.27", Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: dirty},
	}}
}

// Positive: a stamped build records its identity, its full revision, the dirty flag and the
// toolchain. Without a threaded identity the version is what the stamp proves.
func TestEngineBuild_Positive_RecordsTheStamp(t *testing.T) {
	revision := strings.Repeat("b", 40)
	want := map[string]string{"version": strings.Repeat("b", buildid.ShortRevisionLen) + "-dirty",
		"revision": revision, "modified": "true", "go_version": "go1.27"}
	if got := engineBuild(buildid.Identity{}, stampedInfo(revision, true)); !maps.Equal(got, want) {
		t.Fatalf("engineBuild = %v, want %v", got, want)
	}
}

// Positive: a release build records the release the binary was given with -X main.version
// next to the commit it was built from, so the report names what `praetorctl version` prints
// (#689). Before, engine_build held only the revision and the dirty flag.
func TestEngineBuild_Positive_ReleaseBuildRecordsItsVersion(t *testing.T) {
	revision := strings.Repeat("d", 40)
	release := buildid.Identify("v1.4.2", stampedInfo(revision, false))
	got := engineBuild(release, stampedInfo(revision, false))
	want := map[string]string{"version": "v1.4.2", "revision": revision, "modified": "false", "go_version": "go1.27"}
	if !maps.Equal(got, want) {
		t.Fatalf("engineBuild = %v, want %v", got, want)
	}
}

// Negative: an unstamped build, a test binary among them, still produces a complete map. It
// names why nothing identifies it instead of a plausible version, claims no dirty flag and
// keeps the revision "unavailable".
func TestEngineBuild_Negative_Unstamped(t *testing.T) {
	want := map[string]string{"version": "unknown (no build information)", "revision": "unavailable"}
	if got := engineBuild(buildid.Identity{}, nil); !maps.Equal(got, want) {
		t.Fatalf("nil info: %v, want %v", got, want)
	}
	devel := &debug.BuildInfo{GoVersion: "go1.27", Main: debug.Module{Version: "(devel)"}}
	want = map[string]string{"version": "unknown (untagged build, no VCS stamp)", "revision": "unavailable", "go_version": "go1.27"}
	if got := engineBuild(buildid.Identity{}, devel); !maps.Equal(got, want) {
		t.Fatalf("unstamped info: %v, want %v", got, want)
	}
}

// Boundary: a `go install module@version` build carries no VCS stamp, only the module
// version. A tag is recorded as the release; a pseudo-version as the 12-character commit it
// ends with, as `praetorctl version` reports both (#642). The revision stays the VCS stamp's.
func TestEngineBuild_Boundary_ModuleVersionBuild(t *testing.T) {
	for _, tc := range []struct{ module, version string }{
		{module: "v1.4.0", version: "v1.4.0"},
		{module: "v0.0.0-20260925120000-0123456789abcdef", version: "0123456789ab"},
	} {
		info := &debug.BuildInfo{GoVersion: "go1.27", Main: debug.Module{Version: tc.module}}
		got := engineBuild(buildid.Identify("", info), info)
		want := map[string]string{"version": tc.version, "revision": "unavailable", "go_version": "go1.27"}
		if !maps.Equal(got, want) {
			t.Errorf("module %s: engineBuild = %v, want %v", tc.module, got, want)
		}
		if zero := engineBuild(buildid.Identity{}, info); !maps.Equal(zero, got) {
			t.Errorf("module %s: unthreaded identity %v differs from the resolved %v", tc.module, zero, got)
		}
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
	if got := engineBuild(buildid.Identity{}, info)["revision"]; got != revision || got == "unavailable" {
		t.Fatalf("engine_build revision %q, shared reader %q", got, revision)
	}
}

// Positive and negative: the identity threaded through SuiteOptions reaches the persisted
// report, and an unthreaded run from this unstamped test binary still writes a report the
// repair loader accepts, naming what the binary proves.
func TestRunSuiteRecordsTheThreadedEngineIdentity(t *testing.T) {
	bad, good := suiteFixture(t, ""), suiteFixture(t, suiteFixtureRecord)
	good.ID = "good"
	for _, tc := range []struct {
		name   string
		engine buildid.Identity
		want   string
	}{
		{name: "release", engine: buildid.Identity{Release: "v9.8.7-dogfood"}, want: "v9.8.7-dogfood"},
		{name: "unthreaded", want: buildid.Running("").String()},
	} {
		opts := suiteOptions(t, bad, good)
		opts.Engine = tc.engine
		report, err := RunSuite(context.Background(), opts)
		if err == nil {
			t.Fatalf("%s: failure fixture unexpectedly verified", tc.name)
		}
		loaded, err := LoadRepairReport(context.Background(), filepath.Join(report.Options.ArtifactDir, "report.json"))
		if err != nil {
			t.Fatalf("%s: persisted report rejected: %v", tc.name, err)
		}
		if got := loaded.Engine["version"]; got != tc.want || got == "" {
			t.Errorf("%s: engine_build.version %q, want %q", tc.name, got, tc.want)
		}
		if loaded.Options.Engine != (buildid.Identity{}) {
			t.Errorf("%s: options serialized the engine identity: %+v", tc.name, loaded.Options.Engine)
		}
	}
}

// Positive and negative: a discovery report records the threaded identity as a suite report
// does, and an unthreaded plan from this test binary records what the binary proves.
func TestRunDiscoveryRecordsTheThreadedEngineIdentity(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	if err := os.WriteFile(policy, []byte(`{"version":1,"rules":[{"key":"hiss:go","title":"Go","kind":"scanner_extension","matches":[".go"],"analyzer":"hiss"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		engine buildid.Identity
		want   string
	}{
		"release":    {engine: buildid.Identity{Release: "v3.1.0-discovery"}, want: "v3.1.0-discovery"},
		"unthreaded": {want: buildid.Running("").String()},
	} {
		opts := DiscoveryOptions{Path: root, PolicyPath: policy, ArtifactDir: filepath.Join(root, name), Stage: "plan", Concurrency: 1, Engine: tc.engine}
		report, err := RunDiscovery(context.Background(), opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := report.Engine["version"]; got != tc.want || got == "" {
			t.Errorf("%s: engine_build.version %q, want %q", name, got, tc.want)
		}
	}
}

// Positive: a scheduled suite runs as the identity the schedule was given, so its report
// names the same build as a suite the binary runs directly.
func TestScheduledSuiteRunsAsTheGivenEngine(t *testing.T) {
	opts := suiteOptions(t, suiteFixture(t, suiteFixtureRecord+"\n"))
	report, err := suiteRunnerAs(buildid.Identity{Release: "v2.0.0-schedule"})(context.Background(), opts)
	if err != nil || report == nil {
		t.Fatalf("scheduled suite: %+v %v", report, err)
	}
	if got := report.Engine["version"]; got != "v2.0.0-schedule" {
		t.Fatalf("scheduled engine_build.version %q", got)
	}
}
