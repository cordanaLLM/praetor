// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// titledRow is one records row with its title, disposition, milestone and merge time.
func titledRow(number, title, disposition, milestone, merged string) string {
	row := recordRow(number, "feat/"+number, disposition, labelled("1", "measured"))
	row = strings.Replace(row, `"title":"Unit `+number+`"`, `"title":"`+title+`"`, 1)
	return strings.Replace(row, `"merged_at":"2026-10-02T12:00:00Z"`, `"merged_at":"`+merged+`","milestone":"`+milestone+`"`, 1)
}

func collectTitled(t *testing.T, opts CollectorOptions, rows ...string) *Report {
	t.Helper()
	opts.ForgeJSONPath = filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, opts.ForgeJSONPath, []byte("["+strings.Join(rows, ",")+"]"))
	return collect(t, opts)
}

// dispositions maps each unit number to its disposition.
func dispositions(r *Report) map[int]string {
	out := make(map[int]string, len(r.Units))
	for _, u := range r.Units {
		out[u.PullRequestNumber] = u.Disposition
	}
	return out
}

const (
	day1 = "2026-10-02T12:00:00Z"
	day2 = "2026-10-03T12:00:00Z"
	day3 = "2026-10-04T12:00:00Z"
)

func TestCollector_Negative_RevertRuleNeedsQualifiedRevertAndTarget(t *testing.T) {
	cases := map[string]struct {
		rows []string
		want string
	}{
		"rejected revert reverts nothing":  {[]string{titledRow("1", "Add x", "qualified", "M1", day1), titledRow("2", `Revert \"Add x\"`, "rejected", "M1", day2)}, DispositionQualified},
		"explicit target disposition kept": {[]string{titledRow("1", "Add x", "abandoned", "M1", day1), titledRow("2", `Revert \"Add x\"`, "qualified", "M1", day2)}, DispositionAbandoned},
		"bare Revert prefix is no revert":  {[]string{titledRow("1", "button colour", "qualified", "M1", day1), titledRow("2", "Revert button colour", "qualified", "M1", day2)}, DispositionQualified},
		"revert merged before the target":  {[]string{titledRow("1", "Add x", "qualified", "M1", day2), titledRow("2", `Revert \"Add x\"`, "qualified", "M1", day1)}, DispositionQualified},
	}
	for name, c := range cases {
		if got := dispositions(collectTitled(t, CollectorOptions{}, c.rows...))[1]; got != c.want {
			t.Errorf("%s: unit #1 = %q, want %q", name, got, c.want)
		}
	}
}

// A revert marks the landing it followed; a later re-land under the same title stays qualified.
func TestCollector_Positive_RevertRuleSkipsLaterReland(t *testing.T) {
	r := collectTitled(t, CollectorOptions{},
		titledRow("1", "Add x", "qualified", "M1", day1),
		titledRow("2", "revert: Add x", "qualified", "M1", day2),
		titledRow("3", "Add x", "qualified", "M1", day3))
	want := map[int]string{1: DispositionReverted, 2: DispositionQualified, 3: DispositionQualified}
	for n, d := range want {
		if got := dispositions(r)[n]; got != d {
			t.Errorf("unit #%d = %q, want %q", n, got, d)
		}
	}
}

// Reverts come from every loaded record, so a --milestone filter does not hide them. (A --limit
// cut alone never drops a revert while keeping its target: the revert merged later.)
func TestCollector_Positive_RevertOutsideSelectionStillCounts(t *testing.T) {
	r := collectTitled(t, CollectorOptions{Milestone: "M1", Limit: 1},
		titledRow("1", "Add x", "qualified", "M1", day1),
		titledRow("2", `Revert \"Add x\"`, "qualified", "M2", day2))
	if len(r.Units) != 1 || r.Units[0].Disposition != DispositionReverted {
		t.Errorf("a revert outside the milestone must still mark its target: %+v", dispositions(r))
	}
}

func TestCollector_Positive_LiveRevertMarksListedTarget(t *testing.T) {
	merged := func(h int) time.Time { return time.Date(2026, 10, 1, h, 0, 0, 0, time.UTC) }
	stub := &stubForge{list: forge.MergedPullRequestList{PullRequests: []forge.MergedPullRequest{
		{Number: 9, HeadBranch: "r", Title: `Revert "Add x"`, CreatedAt: merged(1), MergedAt: merged(4)},
		{Number: 8, HeadBranch: "x", Title: "Add x", CreatedAt: merged(1), MergedAt: merged(2)},
	}}}
	if got := dispositions(collect(t, CollectorOptions{ForgeDriver: stub})); got[8] != DispositionReverted || got[9] != DispositionQualified {
		t.Errorf("live rows: %+v", got)
	}
}

func TestCollector_Negative_RecordsRowWithoutDispositionRefused(t *testing.T) {
	row := strings.Replace(titledRow("1", "Add x", "qualified", "M1", day1), `"disposition":"qualified",`, "", 1)
	path := filepath.Join(t.TempDir(), "prs.json")
	writeFile(t, path, []byte("["+row+"]"))
	_, err := NewCollector(CollectorOptions{ForgeJSONPath: path}).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "disposition missing") {
		t.Errorf("a records row without a disposition must be refused, not default to qualified: %v", err)
	}
}

func TestRevertedTitle_PositiveNegativeBoundary(t *testing.T) {
	for title, want := range map[string]string{
		`Revert "Add x"`:              "Add x",
		`revert "Add x"`:              "Add x",
		"revert: Add x":               "Add x",
		`Revert "Revert "Add x""`:     `Revert "Add x"`,
		"Revert Add x":                "",
		`Revert "Add x`:               "",
		`Revert ""`:                   "",
		"Reverted the button colours": "",
		"Add x":                       "",
	} {
		if got := revertedTitle(title); got != want {
			t.Errorf("revertedTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
