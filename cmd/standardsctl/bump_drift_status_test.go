package main

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/bump"
)

// Positive, negative and boundary: the inventory status follows the SemVer verdict the
// scanner recorded, not the raw strings; an exact pin inside the registry's major line is
// up to date, a pin behind it drifts, and a deprecated pin says so whatever its version.
func TestActionDriftStatusFollowsSemVerVerdict(t *testing.T) {
	cases := []struct {
		current, latest string
		deprecated      bool
		want            string
	}{
		{"v4.1.2", "v4", false, "[UP-TO-DATE]"},
		{"v3.8.1", "v4.1.2", false, "[DRIFT]"},
		{"v4", "v4", true, "[DEPRECATED]"},
	}
	for _, tc := range cases {
		a := bump.ActionCandidate{
			Action: "example/action", CurrentVersion: tc.current, LatestVersion: tc.latest,
			UpToDate: bump.ActionPinCurrent(tc.current, tc.latest), Deprecated: tc.deprecated,
		}
		if got := bump.ActionDriftStatus(a); got != tc.want {
			t.Errorf("%s -> %s (deprecated %v): %s, want %s", tc.current, tc.latest, tc.deprecated, got, tc.want)
		}
	}
}
