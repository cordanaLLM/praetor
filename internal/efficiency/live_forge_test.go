// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// serveGitHubPulls answers the closed pull request listing with pulls on page 1 and issue
// lookups with a creation time, through the real GitHub driver.
func serveGitHubPulls(t *testing.T, pulls []map[string]any) *forge.GitHubDriver {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var payload any = []map[string]any{}
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/acme/widgets/issues/"):
			payload = map[string]any{"number": 3, "created_at": "2026-10-01T08:00:00Z"}
		case r.URL.Path == "/repos/acme/widgets/pulls" && r.URL.Query().Get("page") == "1":
			payload = pulls
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("encode fake forge response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_REPOSITORY", "")
	gh := forge.NewGitHubDriver("token", srv.URL)
	gh.SetRepository("acme", "widgets")
	return gh
}

// Live rows come from parseGitHubMergedPulls and carry no epoch, disposition or vector field;
// the collector stamps them instead of refusing every live run.
func TestCollector_Positive_LiveGitHubRowsAreStampedNotRefused(t *testing.T) {
	gh := serveGitHubPulls(t, []map[string]any{
		{"number": 7, "title": "Real GitHub PR", "body": "Closes #3", "created_at": "2026-10-01T10:00:00Z",
			"merged_at": "2026-10-01T12:00:00Z", "head": map[string]any{"ref": "feat/live"}},
		{"number": 8, "title": "Closed unmerged", "body": "", "created_at": "2026-10-01T10:00:00Z",
			"head": map[string]any{"ref": "feat/closed"}},
	})
	report, err := NewCollector(CollectorOptions{ForgeDriver: gh}).Collect(context.Background())
	if err != nil {
		t.Fatalf("a live run must not be refused for rows the listing cannot carry: %v", err)
	}
	if len(report.Units) != 1 {
		t.Fatalf("only the merged pull request is a unit: %+v", report.Units)
	}
	u := report.Units[0]
	assertLiveStamped(t, u, 7200)
	if u.IssueToMerge != "4h00m" {
		t.Errorf("issue-to-merge from the fetched closing issue: %q", u.IssueToMerge)
	}
	ms := report.MilestoneSummary
	if want := "2h00m per qualified unit (total 2h00m over 1 units / 1 qualified) [measured 1]"; ms.WallSeconds.Display != want {
		t.Errorf("wall seconds summary = %q, want %q", ms.WallSeconds.Display, want)
	}
	for name, v := range map[string]VectorSummary{"tokens": ms.TokensByProvider, "review": ms.ReviewRounds, "retries": ms.Retries, "minutes": ms.OperatorMinutes, "defects": ms.EscapedDefects} {
		if v.Display != NotMeasured || v.UnitsMeasured != 0 {
			t.Errorf("%s without a live source must print %q: %+v", name, NotMeasured, v)
		}
	}
	notes := strings.Join(report.Notes, "\n")
	for _, want := range []string{LiveUnmeasuredFields, LiveDispositionNote} {
		if !strings.Contains(notes, want) {
			t.Errorf("note %q missing in %q", want, notes)
		}
	}
}

func TestCollector_Boundary_LiveRowWithoutUsableTimestampsLeavesWallSecondsUnmeasured(t *testing.T) {
	stub := &stubForge{list: forge.MergedPullRequestList{PullRequests: []forge.MergedPullRequest{{Number: 9, HeadBranch: "b", Title: "No times"}}}}
	report := collect(t, CollectorOptions{ForgeDriver: stub})
	u := report.Units[0]
	if u.MetricEpoch != CurrentMetricEpoch || u.WallSeconds != nil {
		t.Errorf("no timestamps: epoch %q wall %+v", u.MetricEpoch, u.WallSeconds)
	}
	if report.MilestoneSummary.WallSeconds.Display != NotMeasured {
		t.Errorf("summary = %q", report.MilestoneSummary.WallSeconds.Display)
	}
}
