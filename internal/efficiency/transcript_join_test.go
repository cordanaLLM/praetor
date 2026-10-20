// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

func toolLine(t *testing.T, id, branch, tool string, input map[string]string, tokens int) string {
	t.Helper()
	line := map[string]any{"type": "assistant", "cwd": "/checkout", "gitBranch": branch, "sessionId": "shared", "requestId": "r-" + id,
		"message": map[string]any{"id": id, "model": "claude-opus-4-1", "usage": map[string]int{"input_tokens": tokens},
			"content": []any{map[string]any{"type": "tool_use", "id": "tool-" + id, "name": tool, "input": input}}}}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func plantedWorktreeTranscript(t *testing.T) string {
	t.Helper()
	creation := toolLine(t, "create", "main", "Bash", map[string]string{"command": "git worktree add -b feat/a /w/praetor-a main; git worktree add -b feat/b /w/praetor-b main"}, 10)
	a := toolLine(t, "a", "main", "Bash", map[string]string{"command": "cd /w/praetor-a && go test ./..."}, 20)
	b := toolLine(t, "b", "main", "Edit", map[string]string{"file_path": "/w/praetor-b/internal/x.go"}, 30)
	neither := toolLine(t, "none", "main", "Bash", map[string]string{"command": "date"}, 40)
	both := toolLine(t, "both", "main", "Bash", map[string]string{"command": "git -C /w/praetor-a status; git -C /w/praetor-b status"}, 50)
	// Creations occur last to prove file/line order does not decide the root mapping.
	return a + a + b + neither + both + creation
}

func TestEfficiencyTranscriptToolInputTwoWorktrees(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "session.jsonl"), []byte(plantedWorktreeTranscript(t)))
	driver := &stubForge{list: forge.MergedPullRequestList{PullRequests: []forge.MergedPullRequest{
		{Number: 1, HeadBranch: "feat/a"}, {Number: 2, HeadBranch: "feat/b"}, {Number: 3, HeadBranch: "missing"},
	}}}
	report := collect(t, CollectorOptions{Root: dir, DefaultBranch: "main", ForgeDriver: driver, TranscriptsDir: dir})
	for number, tokens := range map[int]int64{1: 20, 2: 30} {
		unit := unitByNumber(t, report, number)
		if unit.FrontierTokensNum == nil || *unit.FrontierTokensNum != tokens {
			t.Fatalf("PR %d: tokens=%s, want %d", number, unit.FrontierTokens, tokens)
		}
		share := unit.TranscriptCoverage
		if share == nil || share.UnattributedResponses != 3 || share.UnattributedTokens != 100 {
			t.Fatalf("PR %d coverage: %+v", number, share)
		}
	}
	share := report.TranscriptCoverage
	if share == nil || *share != (TranscriptShare{Responses: 5, Tokens: 150, UnattributedResponses: 3, UnattributedTokens: 100}) {
		t.Fatalf("unique total: %+v", share)
	}
	missing := unitByNumber(t, report, 3)
	if missing.FrontierTokens != NotMeasured || missing.TokenMeasurementReason == "" {
		t.Fatalf("missing reason: %+v", missing)
	}
	checkTranscriptReportFormats(t, report)
}

func checkTranscriptReportFormats(t *testing.T, report *Report) {
	t.Helper()
	var table, data bytes.Buffer
	if err := RenderTable(report, &table); err != nil {
		t.Fatal(err)
	}
	if err := RenderJSON(report, &data); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"UNATTRIBUTED TRANSCRIPT USAGE", "100/150 tokens", "no response joined", "transcript worktree creations=2"} {
		if !strings.Contains(table.String(), text) {
			t.Errorf("table missing %q", text)
		}
	}
	var decoded Report
	if err := json.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TranscriptCoverage == nil || *decoded.TranscriptCoverage != *report.TranscriptCoverage {
		t.Fatal("JSON lost coverage")
	}
	if decoded.Units[2].TokenMeasurementReason == "" {
		t.Fatal("JSON lost measurement reason")
	}
}

func TestEfficiencyTranscriptToolInputNegativeAndBoundaryPaths(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		roots         map[string]string
		want          string
	}{
		{"cd", "cd /w/praetor-a", map[string]string{"/w/praetor-a": "a"}, "a"},
		{"git-C", "git -C /w/praetor-a status", map[string]string{"/w/praetor-a": "a"}, "a"},
		{"absolute", "cat /w/praetor-a/file", map[string]string{"/w/praetor-a": "a"}, "a"},
		{"unknown", "cat /unknown/file", map[string]string{"/w/praetor-a": "a"}, ""},
		{"unresolved", "cd /w/praetor-a", map[string]string{"/w/praetor-a": ""}, ""},
		{"prefix", "cd /w/praetor-ab", map[string]string{"/w/praetor-a": "a"}, ""},
		{"longest", "cd /w/praetor-a/sub", map[string]string{"/w/praetor-a": "a", "/w/praetor-a/sub": "b"}, "b"},
		{"longest-unresolved", "cd /w/praetor-a/sub", map[string]string{"/w/praetor-a": "a", "/w/praetor-a/sub": ""}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(toolLine(t, "m", "main", "Bash", map[string]string{"command": tc.command}, 7)))
			stats, notes, err := readJoinedTranscripts(t.Context(), dir, NewClassifier(nil), defaultLimits(), newTranscriptJoin(tc.roots, "main"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if notes.Coverage.UnattributedTokens != 7 {
					t.Fatalf("unresolved usage dropped: %+v", notes)
				}
				return
			}
			if stats[tc.want] == nil || stats[tc.want].FrontierTokens != 7 {
				t.Fatalf("branch %s: %+v", tc.want, stats)
			}
		})
	}
}

func TestEfficiencyTranscriptToolInputBranchPrecedence(t *testing.T) {
	dir := t.TempDir()
	line := toolLine(t, "m", "feat/direct", "Edit", map[string]string{"file_path": "/w/other/file"}, 9)
	writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(line+line))
	stats, notes, err := readJoinedTranscripts(t.Context(), dir, NewClassifier(nil), defaultLimits(), newTranscriptJoin(map[string]string{"/w/other": "feat/other"}, "main"))
	if err != nil {
		t.Fatal(err)
	}
	if stats["feat/direct"].FrontierTokens != 9 || notes.Coverage.UnattributedResponses != 0 {
		t.Fatalf("explicit branch must win once: %+v", stats)
	}
}

func TestEfficiencyTranscriptToolInputByteBoundary(t *testing.T) {
	for _, size := range []int{MaxResponseToolBytes, MaxResponseToolBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			// JSON input is {"command":"..."}: fourteen bytes of envelope.
			line := toolLine(t, "m", "main", "Bash", map[string]string{"command": strings.Repeat("x", size-14)}, 1)
			stats := make(map[string]*BranchTranscriptStats)
			notes, err := ReadTranscriptStream(t.Context(), "cap", strings.NewReader(line+line), NewClassifier(nil), stats)
			if size > MaxResponseToolBytes {
				if err == nil || !strings.Contains(err.Error(), "byte bound") {
					t.Fatalf("want explicit overflow: %v", err)
				}
				return
			}
			if err != nil || notes.Coverage.Responses != 1 {
				t.Fatalf("at cap/replay: %v %+v", err, notes)
			}
		})
	}
}

func TestEfficiencyTranscriptLiveWorktreeSource(t *testing.T) {
	dir := t.TempDir()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-b", "trunk"}, {"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "init"}, {"worktree", "add", "-b", "feat/live", filepath.Join(dir, "live")}} {
		if out, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	c := NewCollector(CollectorOptions{Root: dir, DefaultBranch: "trunk"})
	join, notes, err := c.transcriptJoin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.ToSlash(filepath.Join(dir, "live"))
	if err := join.recordRoot(root, "stale/transcript"); err != nil {
		t.Fatal(err)
	}
	if join.roots[root] != "feat/live" || join.defaultBranch != "trunk" || len(notes) != 0 {
		t.Fatalf("live source precedence: %+v %v", join, notes)
	}
}

func TestEfficiencyTranscriptRecordedCreationShapes(t *testing.T) {
	for _, tc := range []struct{ command, root, branch string }{
		{"git worktree add -B feat/reset /w/a main", "/w/a", "feat/reset"},
		{"git worktree add /w/a feat/existing", "/w/a", "feat/existing"},
		{"git -C /repo worktree add -b feat/relative ../a main", "/a", "feat/relative"},
		{"cd /repo && git worktree add -b feat/relative ../a main", "/a", "feat/relative"},
		{"git worktree add -b 'feat/quoted' '/w/with space' main", "/w/with space", "feat/quoted"},
		{"git worktree add /w/a", "/w/a", ""},
		{"git worktree add /w/a HEAD~2", "/w/a", ""},
		{"git worktree add /w/a abc1234", "/w/a", ""},
		{"echo git worktree add -b fake /w/a", "", ""},
		{"echo cd /wrong; git worktree add -b feat/x a main", "/checkout/a", "feat/x"},
		{"git worktree add -b feat/x $ROOT", "", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			j := newTranscriptJoin(nil, "main")
			if err := j.bashPaths(tc.command, "/checkout", make(map[string]bool)); err != nil {
				t.Fatal(err)
			}
			if tc.root == "" {
				if len(j.roots) != 0 {
					t.Fatalf("inferred unsupported creation: %v", j.roots)
				}
				return
			}
			if branch, ok := j.roots[tc.root]; !ok || branch != tc.branch {
				t.Fatalf("roots=%v want %s -> %s", j.roots, tc.root, tc.branch)
			}
		})
	}
}

func TestEfficiencyTranscriptStreamedToolsDedupeAndAmbiguity(t *testing.T) {
	first := toolLine(t, "same", "main", "Edit", map[string]string{"file_path": "/w/a/file"}, 10)
	second := toolLine(t, "same", "main", "Read", map[string]string{"file_path": "/w/b/file"}, 20)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(first+second+second))
	_, notes, err := readJoinedTranscripts(t.Context(), dir, NewClassifier(nil), defaultLimits(), newTranscriptJoin(map[string]string{"/w/a": "a", "/w/b": "b"}, "main"))
	if err != nil {
		t.Fatal(err)
	}
	if notes.Coverage.UnattributedResponses != 1 || notes.Coverage.UnattributedTokens != 20 {
		t.Fatalf("all blocks must decide one response: %+v", notes.Coverage)
	}
}

func TestEfficiencyTranscriptRootEvidenceConflicts(t *testing.T) {
	j := newTranscriptJoin(nil, "main")
	for _, branch := range []string{"", "a", "", "b", "a"} {
		if err := j.recordRoot("/w/a", branch); err != nil {
			t.Fatal(err)
		}
	}
	if j.roots["/w/a"] != "" {
		t.Fatalf("conflicting history must stay unresolved: %v", j.roots)
	}
	if err := j.recordRoot("/w/b", ""); err != nil {
		t.Fatal(err)
	}
	if err := j.recordRoot("/w/b", "b"); err != nil {
		t.Fatal(err)
	}
	if j.roots["/w/b"] != "b" {
		t.Fatal("missing evidence must not override explicit evidence")
	}
}

func TestEfficiencyTranscriptToolInputCumulativeByteBound(t *testing.T) {
	first := toolLine(t, "m", "main", "Bash", map[string]string{"command": strings.Repeat("a", MaxResponseToolBytes/2)}, 1)
	second := toolLine(t, "m", "main", "Bash", map[string]string{"command": strings.Repeat("b", MaxResponseToolBytes/2)}, 1)
	_, err := ReadTranscriptStream(t.Context(), "cap", strings.NewReader(first+second), NewClassifier(nil), make(map[string]*BranchTranscriptStats))
	if err == nil || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("response-wide cap lost: %v", err)
	}
}

func TestEfficiencyTranscriptFileToolShapes(t *testing.T) {
	for _, tc := range []struct{ name, field string }{
		{"Read", "file_path"}, {"Edit", "file_path"}, {"Write", "file_path"},
		{"NotebookEdit", "file_path"}, {"NotebookEdit", "notebook_path"}, {"EnterWorktree", "path"},
	} {
		t.Run(tc.name+tc.field, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "s.jsonl"), []byte(toolLine(t, "m", "main", tc.name, map[string]string{tc.field: "/w/a/file"}, 3)))
			stats, _, err := readJoinedTranscripts(t.Context(), dir, NewClassifier(nil), defaultLimits(), newTranscriptJoin(map[string]string{"/w/a": "a"}, "main"))
			if err != nil {
				t.Fatal(err)
			}
			if stats["a"] == nil || stats["a"].FrontierTokens != 3 {
				t.Fatalf("tool path not joined: %v", stats)
			}
		})
	}
}

func TestEfficiencyTranscriptBashAbsoluteArguments(t *testing.T) {
	for _, command := range []string{"tool --file=/w/a/file", `tool --file="/w/a/file"`, `python -c "x='/w/a/file'"`, "git -C ../a status", `python -c "print('/w/a/file')"`} {
		t.Run(command, func(t *testing.T) {
			paths := make(map[string]bool)
			j := newTranscriptJoin(nil, "main")
			if err := j.bashPaths(command, "/w/checkout", paths); err != nil {
				t.Fatal(err)
			}
			if !paths["/w/a/file"] && !paths["/w/a"] {
				t.Fatalf("absolute argument missing: %v", paths)
			}
		})
	}
}

func TestEfficiencyTranscriptUnknownToolRemainsUnattributed(t *testing.T) {
	data := `{"type":"assistant","gitBranch":"main","message":{"id":"m","model":"claude-opus-4-1","usage":{"input_tokens":4},"content":[{"type":"tool_use","name":"custom","input":{"command":["/w/a/file"]}}]}}`
	notes, err := ReadTranscriptStream(t.Context(), "unknown", strings.NewReader(data), NewClassifier(nil), make(map[string]*BranchTranscriptStats))
	if err != nil {
		t.Fatal(err)
	}
	if notes.Coverage.UnattributedTokens != 4 {
		t.Fatalf("unknown tool lost usage: %+v", notes.Coverage)
	}
}

func TestEfficiencyTranscriptQuotedMultiPathIsAmbiguous(t *testing.T) {
	j := newTranscriptJoin(map[string]string{"/w/a": "a", "/w/b": "b"}, "main")
	paths := make(map[string]bool)
	if err := j.bashPaths(`echo "/w/a /w/b"`, "/checkout", paths); err != nil {
		t.Fatal(err)
	}
	if branches := j.resolve(paths); len(branches) != 2 {
		t.Fatalf("quoted paths must retain ambiguity: %v %v", paths, branches)
	}
}
