// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"testing"
)

// Positive: every prerelease marker keeps a version off the stable channel, including the
// markers the old substring switch missed (BUG-421), and known markers keep their channel.
func TestClassifyChannelPrereleaseMarkers(t *testing.T) {
	cases := map[string]ReleaseChannel{
		"1.2.3":                                ChannelStable,
		"v1.2.3+build.5":                       ChannelStable,
		"1.2.3-rc.1":                           ChannelRC,
		"v2.0.0-beta.2":                        ChannelBeta,
		"3.0.0-alpha":                          ChannelAlpha,
		"1.2.3-next.1":                         ChannelNightly,
		"1.2.3-canary.20260925":                ChannelNightly,
		"1.2.3-pre":                            ChannelNightly,
		"1.2.3-snapshot":                       ChannelNightly,
		"1.2.3-SNAPSHOT":                       ChannelNightly,
		"v0.0.0-20260925120000-abcdef123456":   ChannelNightly,
		"v1.2.4-0.20260925120000-abcdef123456": ChannelNightly,
	}
	for version, want := range cases {
		if got := ClassifyChannel(version); got != want {
			t.Errorf("ClassifyChannel(%q) = %q, want %q", version, got, want)
		}
	}
}

// Negative: a version string that is not SemVer falls back to the marker table, so an
// unmarked loose version stays stable and a marked one does not.
func TestClassifyChannelLooseVersions(t *testing.T) {
	cases := map[string]ReleaseChannel{
		"1.2":           ChannelStable,
		"latest":        ChannelStable,
		"2.1-canary":    ChannelNightly,
		"1.0-snapshot2": ChannelNightly,
		"4.0-rc2":       ChannelRC,
	}
	for version, want := range cases {
		if got := ClassifyChannel(version); got != want {
			t.Errorf("ClassifyChannel(%q) = %q, want %q", version, got, want)
		}
	}
}

// Positive, negative and boundary: an action pin is compared by SemVer at the precision of
// the less precise tag, never as a raw string (BUG-425).
func TestActionPinCurrent(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v4.1.2", "v4", true},        // exact pin inside the registry's major line
		{"v4", "v4", true},            // bare major against bare major
		{"v4", "v4.1.2", true},        // moving major tag follows every v4.x release
		{"v5", "v4", true},            // ahead of a lagging registry is not drift
		{"v3.8.1", "v4.1.2", false},   // the pre-upgrade cosign-installer pin
		{"v0.24.2", "v0.24.2", true},  // exact pin equal to an exact registry entry
		{"v0.18.0", "v0.24.2", false}, // exact pin behind an exact registry entry
		{"v4.1", "v4.1.2", true},      // major.minor tag follows its patch releases
		{"v4.0", "v4.1.2", false},     // an older minor line
		{"v4.1.2-rc.1", "v4.1.2", false},
		{"v3", "v4", false},
		{"8e5e7e5ab8b370d6c329ec480221332ada57f0ab", "v4", false}, // SHA pin: not comparable
		{"main", "main", true},
	}
	for _, tc := range cases {
		if got := ActionPinCurrent(tc.current, tc.latest); got != tc.want {
			t.Errorf("ActionPinCurrent(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

// Negative: a commented-out `uses:` line, whole-line or trailing, is not scanned as a pin,
// and a live pin with a trailing comment keeps its version.
func TestScanWorkflowActionsIgnoresCommentedUses(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "jobs:\n  build:\n    steps:\n" +
		"      # - uses: actions/checkout@v1\n" +
		"      - uses: actions/checkout@v4.1.2 # uses: actions/setup-go@v1\n"
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	actions, deprecations, err := ScanWorkflowActions(t.Context(), root)
	if err != nil {
		t.Fatalf("ScanWorkflowActions: %v", err)
	}
	if len(actions) != 1 || actions[0].Action != "actions/checkout" || actions[0].CurrentVersion != "v4.1.2" {
		t.Fatalf("actions = %+v, want only the live actions/checkout@v4.1.2", actions)
	}
	if !actions[0].UpToDate {
		t.Errorf("actions/checkout@v4.1.2 against registry %s reported drift", actions[0].LatestVersion)
	}
	if len(deprecations) != 0 {
		t.Errorf("a commented-out pin raised deprecations: %+v", deprecations)
	}
}
