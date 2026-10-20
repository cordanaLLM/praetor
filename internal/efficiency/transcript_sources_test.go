// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestEfficiencyTranscriptUnmeasuredReasons(t *testing.T) {
	dir := t.TempDir()
	creation := toolLine(t, "create", "setup", "Bash", map[string]string{"command": "git worktree add -b a /w/a main; git worktree add -b b /w/b main; git worktree add -b idle /w/idle main"}, 1)
	both := toolLine(t, "both", "main", "Bash", map[string]string{"command": "cat /w/a/file /w/b/file"}, 20)
	only := toolLine(t, "only", "main", "Read", map[string]string{"file_path": "/w/measured/file"}, 7)
	creation += toolLine(t, "measured-create", "setup", "Bash", map[string]string{"command": "git worktree add -b measured /w/measured main"}, 1)
	writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(creation+both+only))
	driver := &stubForge{list: forge.MergedPullRequestList{PullRequests: []forge.MergedPullRequest{
		{Number: 1, HeadBranch: "missing"}, {Number: 2, HeadBranch: "a"}, {Number: 3, HeadBranch: "b"}, {Number: 4, HeadBranch: "idle"}, {Number: 5, HeadBranch: "measured"},
	}}}
	report := collect(t, CollectorOptions{Root: dir, DefaultBranch: "main", ForgeDriver: driver, TranscriptsDir: dir})
	for _, tc := range []struct {
		number int
		reason string
	}{{1, "worktree never resolved"}, {2, "ambiguous"}, {3, "ambiguous"}, {4, "no transcript response"}} {
		u := unitByNumber(t, report, tc.number)
		if u.FrontierTokens != NotMeasured || u.FrontierTokensNum != nil || !strings.Contains(u.TokenMeasurementReason, tc.reason) {
			t.Errorf("PR %d: tokens=%s reason=%q; want %q", tc.number, u.FrontierTokens, u.TokenMeasurementReason, tc.reason)
		}
	}
	if u := unitByNumber(t, report, 5); u.TokenMeasurementReason != "" || u.FrontierTokensNum == nil || *u.FrontierTokensNum != 7 {
		t.Errorf("measured PR: %+v", u)
	}
	if s := report.TranscriptCoverage; s.UnattributedResponses != 1 || s.UnattributedTokens != 20 {
		t.Fatalf("ambiguous usage must remain unattributed: %+v", s)
	}
	checkUnmeasuredReasonFormats(t, report)
}

func checkUnmeasuredReasonFormats(t *testing.T, report *Report) {
	t.Helper()
	var table, data bytes.Buffer
	if err := RenderTable(report, &table); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSON(report, &data); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, u := range report.Units {
		if !strings.Contains(table.String(), u.TokenMeasurementReason) || unitByNumber(t, &decoded, u.PullRequestNumber).TokenMeasurementReason != u.TokenMeasurementReason {
			t.Errorf("PR %d: reason lost in table or JSON", u.PullRequestNumber)
		}
	}
}
