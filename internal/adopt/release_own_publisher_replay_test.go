// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// jellysinReplayWorkflows are the workflows of jellysin/.github at 1e92384 (#45), kept under
// testdata/release-publisher/workflows (see its README).
var jellysinReplayWorkflows = []string{
	"build-signed.yml", "ci.yml", "codeql.yml", "prepare-release.yml", "release-automation.yml", "release.yml",
}

// maxReplayFixtureBytes bounds one replay fixture read (HISS-02).
const maxReplayFixtureBytes = 1 << 20

// TestOwnReleasePublisherJellysinReplay replays the case the check exists for: jellysin/.github
// publishes releases from its own workflow, and before #45 its release-please configuration did
// not set skip-github-release, so release-please published v0.1.0 without assets. That
// configuration fails naming the publishing workflow; #45's configuration passes.
func TestOwnReleasePublisherJellysinReplay(t *testing.T) {
	cases := []struct{ config, want string }{
		{"release-please-config.pre-45.json", "[FAIL] " + releaseOwnPublisherRule},
		{"release-please-config.45.json", "[PASS] " + releaseOwnPublisherRule},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.config, func(t *testing.T) {
			files := jellysinReplayFiles(t)
			files["release-please-config.json"] = readReplayFixture(t, tc.config)
			out, err := AuditOwnReleasePublisher(t.Context(), supplyChainRepo(t, files))
			if err != nil {
				out = err.Error()
			}
			if (err != nil) != strings.HasPrefix(tc.want, "[FAIL]") || !strings.HasPrefix(out, tc.want) {
				t.Fatalf("audit = %q, error %v; want prefix %q", out, err, tc.want)
			}
			if !strings.Contains(out, ".github/workflows/release.yml") {
				t.Errorf("the finding must name the publishing workflow release.yml: %s", out)
			}
		})
	}
}

// jellysinReplayFiles returns the replayed workflows keyed by their path in a repository.
func jellysinReplayFiles(t *testing.T) map[string]string {
	t.Helper()
	files := make(map[string]string, len(jellysinReplayWorkflows)+1)
	for i := 0; i < len(jellysinReplayWorkflows); i++ {
		name := jellysinReplayWorkflows[i]
		files[".github/workflows/"+name] = readReplayFixture(t, filepath.Join("workflows", name))
	}
	return files
}

// readReplayFixture reads one bounded file under testdata/release-publisher.
func readReplayFixture(t *testing.T, rel string) string {
	t.Helper()
	data, err := util.ReadFileLimited(filepath.Join("testdata", "release-publisher", rel), maxReplayFixtureBytes)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
