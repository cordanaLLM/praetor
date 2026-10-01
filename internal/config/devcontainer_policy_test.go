package config

import (
	"fmt"
	"strings"
	"testing"
)

func loadDevContainerManifest(t *testing.T, section string) (*Manifest, error) {
	t.Helper()
	return LoadManifest(writeManifest(t, "version: 1\n"+section))
}

// Positive: an absent section keeps both defaults, a declared bound is taken as written with
// the other defaulted, and the section survives RenderManifest, which adoption rewrites the
// manifest with.
func TestDevContainerFreshnessPositive(t *testing.T) {
	m, err := loadDevContainerManifest(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if commits, days := m.DevContainer.FreshnessBounds(); m.DevContainer != nil || commits != DefaultFreshnessMaxCommits || days != DefaultFreshnessMaxAgeDays {
		t.Fatalf("absent devcontainer section = %+v (%d, %d), want the defaults", m.DevContainer, commits, days)
	}
	m, err = loadDevContainerManifest(t, "devcontainer:\n  freshness:\n    max_commits: 250\n")
	if err != nil {
		t.Fatal(err)
	}
	if commits, days := m.DevContainer.FreshnessBounds(); commits != 250 || days != DefaultFreshnessMaxAgeDays {
		t.Fatalf("declared max_commits read as (%d, %d)", commits, days)
	}
	m, err = loadDevContainerManifest(t, "devcontainer:\n  freshness:\n    max_commits: 250\n    max_age_days: 7\n")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadManifest(writeManifest(t, string(rendered)))
	if err != nil {
		t.Fatal(err)
	}
	if commits, days := again.DevContainer.FreshnessBounds(); commits != 250 || days != 7 {
		t.Fatalf("rendered devcontainer section read back as (%d, %d):\n%s", commits, days, rendered)
	}
}

// Negative: a bound outside its range, a value that is not an integer, and an unknown or
// repeated key are refused, so a typo cannot silently fall back to the default.
func TestDevContainerFreshnessNegative(t *testing.T) {
	cases := map[string]struct{ section, want string }{
		"zero commits":        {"\n    max_commits: 0\n", "max_commits must be an integer from 1 to"},
		"negative age":        {"\n    max_age_days: -1\n", "max_age_days must be an integer from 1 to"},
		"quoted integer":      {"\n    max_commits: \"5\"\n", "max_commits must be an integer"},
		"fractional days":     {"\n    max_age_days: 1.5\n", "max_age_days must be an integer"},
		"unknown key":         {"\n    max_count: 5\n", "unknown or duplicated devcontainer.freshness field \"max_count\""},
		"repeated key":        {"\n    max_commits: 5\n    max_commits: 6\n", "unknown or duplicated devcontainer.freshness field \"max_commits\""},
		"sequence not object": {" [1, 2]\n", "devcontainer.freshness must be a mapping"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadDevContainerManifest(t, "devcontainer:\n  freshness:"+tc.section); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
	if _, err := loadDevContainerManifest(t, "devcontainer:\n  refresh: weekly\n"); err == nil {
		t.Fatal("an unknown devcontainer key was accepted")
	}
}

// Boundary: 1 and each ceiling are accepted, one past a ceiling is refused.
func TestDevContainerFreshnessBoundary(t *testing.T) {
	for _, tc := range []struct {
		commits, days int
		ok            bool
	}{
		{1, 1, true},
		{FreshnessMaxCommitsCeiling, FreshnessMaxAgeDaysCeiling, true},
		{FreshnessMaxCommitsCeiling + 1, 1, false},
		{1, FreshnessMaxAgeDaysCeiling + 1, false},
	} {
		section := fmt.Sprintf("devcontainer:\n  freshness:\n    max_commits: %d\n    max_age_days: %d\n", tc.commits, tc.days)
		m, err := loadDevContainerManifest(t, section)
		if (err == nil) != tc.ok {
			t.Fatalf("(%d, %d): err %v, want ok %v", tc.commits, tc.days, err, tc.ok)
		}
		if !tc.ok {
			continue
		}
		if commits, days := m.DevContainer.FreshnessBounds(); commits != tc.commits || days != tc.days {
			t.Fatalf("(%d, %d) read back as (%d, %d)", tc.commits, tc.days, commits, days)
		}
	}
	empty, err := loadDevContainerManifest(t, "devcontainer: {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if commits, days := empty.DevContainer.FreshnessBounds(); commits != DefaultFreshnessMaxCommits || days != DefaultFreshnessMaxAgeDays {
		t.Fatalf("an empty devcontainer section read as (%d, %d), want the defaults", commits, days)
	}
}
