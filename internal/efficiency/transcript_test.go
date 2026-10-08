// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

func readFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "session_real_shapes.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The fixture carries the record shapes of a real session: stop_hook_summary with toolUseID,
// task-notification and peer origins, isMeta, isCompactSummary, a human sidechain prompt, a
// tool result, an attachment and a three-block response sharing message.id and requestId.
func TestReadTranscriptStream_Positive_RealShapes(t *testing.T) {
	byBranch := map[string]*BranchTranscriptStats{}
	notes, err := ReadTranscriptStream(context.Background(), "fixture", bytes.NewReader(readFixture(t)), NewClassifier(nil), byBranch)
	if err != nil {
		t.Fatal(err)
	}
	if notes.SkippedLines != 0 {
		t.Fatalf("real shapes must all decode: %v", notes.Lines())
	}
	s := byBranch["feat/x"]
	if s == nil {
		t.Fatal("branch not joined")
	}
	checkRealShapeCounts(t, s)
	checkRealShapeTokens(t, s)
}

func checkRealShapeCounts(t *testing.T, s *BranchTranscriptStats) {
	t.Helper()
	if s.OperatorTouches != 2 {
		t.Errorf("touches = %d, want 2 (two human prompts; meta, compact, sidechain, task-notification, peer, tool result excluded)", s.OperatorTouches)
	}
	if s.TotalRequests != 2 {
		t.Errorf("requests = %d, want 2 (three lines of msg_1 count once)", s.TotalRequests)
	}
	if s.LocalRequests != 0 {
		t.Errorf("local requests = %d", s.LocalRequests)
	}
}

func checkRealShapeTokens(t *testing.T, s *BranchTranscriptStats) {
	t.Helper()
	if s.FrontierTokens != 10+100+1000+40 {
		t.Errorf("frontier tokens = %d, want 1150 (msg_1 once, final output count)", s.FrontierTokens)
	}
	if s.InputTokens != 17 || s.CacheCreationTokens != 100 || s.CacheReadTokens != 1000 || s.OutputTokens != 43 {
		t.Errorf("token split: %+v", s)
	}
	if rate, ok := s.PromptCacheHitRate(); !ok || rate < 0.895 || rate > 0.896 {
		t.Errorf("cache rate %v %v", rate, ok)
	}
}

func TestReadTranscriptStream_Positive_UsageGrowsAcrossLinesOfOneResponse(t *testing.T) {
	line := func(out int) string {
		return fmt.Sprintf(`{"type":"assistant","requestId":"r","sessionId":"s","gitBranch":"b","message":{"id":"m","model":"claude-opus-4-1","usage":{"input_tokens":1,"output_tokens":%d}}}`, out)
	}
	byBranch := map[string]*BranchTranscriptStats{}
	data := line(5) + "\n" + line(40) + "\n" + line(40) + "\n"
	if _, err := ReadTranscriptStream(context.Background(), "s", strings.NewReader(data), NewClassifier(nil), byBranch); err != nil {
		t.Fatal(err)
	}
	s := byBranch["b"]
	if s.OutputTokens != 40 || s.FrontierTokens != 41 || s.TotalRequests != 1 {
		t.Fatalf("a response is its final usage once: %+v", s)
	}
}

func TestReadTranscriptStream_Negative_UndecodableLinesAreReported(t *testing.T) {
	good := `{"type":"user","origin":{"kind":"human"},"sessionId":"s","gitBranch":"b"}`
	data := good + "\n" + `{"type":"user","x":"\ud800","gitBranch":"b"}` + "\n" + `not json` + "\n" + good + "\n"
	byBranch := map[string]*BranchTranscriptStats{}
	notes, err := ReadTranscriptStream(context.Background(), "sess.jsonl", strings.NewReader(data), NewClassifier(nil), byBranch)
	if err != nil {
		t.Fatal(err)
	}
	if byBranch["b"].OperatorTouches != 2 || notes.SkippedLines != 2 {
		t.Fatalf("touches %d skipped %d", byBranch["b"].OperatorTouches, notes.SkippedLines)
	}
	lines := strings.Join(notes.Lines(), "\n")
	if !strings.Contains(lines, "sess.jsonl:2:") || !strings.Contains(lines, "sess.jsonl:3:") {
		t.Fatalf("notes must name source and line: %s", lines)
	}
}

func TestReadTranscriptStream_Negative_NonMatchingBranchAndNoBranch(t *testing.T) {
	data := `{"type":"user","origin":{"kind":"human"},"gitBranch":"other"}` + "\n" + `{"type":"user","origin":{"kind":"human"}}` + "\n"
	byBranch := map[string]*BranchTranscriptStats{}
	if _, err := ReadTranscriptStream(context.Background(), "s", strings.NewReader(data), NewClassifier(nil), byBranch); err != nil {
		t.Fatal(err)
	}
	if len(byBranch) != 1 || byBranch["other"] == nil {
		t.Fatalf("only lines with a branch join: %v", byBranch)
	}
}

func TestReadTranscriptsDir_Positive_SubagentsDescendedAndHiddenSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.jsonl"), readFixture(t))
	writeFile(t, filepath.Join(dir, "a", "subagents", "agent-1.jsonl"), []byte(`{"type":"user","origin":{"kind":"human"},"gitBranch":"fix/sub"}`+"\n"))
	writeFile(t, filepath.Join(dir, ".hidden", "x.jsonl"), []byte(`{"type":"user","origin":{"kind":"human"},"gitBranch":"hidden"}`+"\n"))
	writeFile(t, filepath.Join(dir, "notes.txt"), []byte("ignored"))
	byBranch, notes, err := ReadTranscriptsDir(context.Background(), dir, NewClassifier(nil))
	if err != nil || notes.SkippedLines != 0 {
		t.Fatalf("%v %v", err, notes)
	}
	if byBranch["fix/sub"] == nil || byBranch["feat/x"] == nil || byBranch["hidden"] != nil {
		t.Fatalf("branches: %v", byBranch)
	}
}

func TestReadTranscriptsDir_Negative_EmptyMissingAndCancelled(t *testing.T) {
	if _, _, err := ReadTranscriptsDir(context.Background(), "  ", NewClassifier(nil)); err == nil {
		t.Error("empty dir must fail")
	}
	if _, _, err := ReadTranscriptsDir(context.Background(), filepath.Join(t.TempDir(), "missing"), NewClassifier(nil)); err == nil {
		t.Error("missing dir must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ReadTranscriptsDir(ctx, t.TempDir(), NewClassifier(nil)); err == nil {
		t.Error("cancelled context must fail")
	}
	unreadable := t.TempDir()
	writeFile(t, filepath.Join(unreadable, "a.jsonl"), []byte("{}\n"))
	if err := os.Chmod(filepath.Join(unreadable, "a.jsonl"), 0); err == nil && os.Getuid() != 0 {
		if _, _, err := ReadTranscriptsDir(context.Background(), unreadable, NewClassifier(nil)); err == nil {
			t.Error("an unreadable file must fail the read, not be skipped")
		}
	}
}

func TestReadTranscriptsDir_Boundary_CapsFailTheRead(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%d.jsonl", i)), []byte("{}\n{}\n"))
	}
	c := NewClassifier(nil)
	cases := map[string]struct {
		limits sourceLimits
		want   string
	}{
		"files at the cap pass": {sourceLimits{Files: 3, Lines: 2, Bytes: 6}, ""},
		"file count over cap":   {sourceLimits{Files: 2, Lines: 10, Bytes: 100}, "exceeds 2 files limit"},
		"line count over cap":   {sourceLimits{Files: 10, Lines: 1, Bytes: 100}, "exceeds 1 record bound"},
		"byte count over cap":   {sourceLimits{Files: 10, Lines: 10, Bytes: 5}, "exceeds 5 byte bound"},
	}
	for name, tc := range cases {
		_, _, err := readTranscriptsDir(context.Background(), dir, c, tc.limits)
		if tc.want == "" && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

func TestReadTranscriptStream_Boundary_LineOverEightMiBFails(t *testing.T) {
	long := bytes.Repeat([]byte("a"), harvester.MaxTranscriptLineBytes+16)
	_, err := ReadTranscriptStream(context.Background(), "big", bytes.NewReader(long), NewClassifier(nil), map[string]*BranchTranscriptStats{})
	if err == nil || !strings.Contains(err.Error(), "big") {
		t.Fatalf("an oversized line is an error naming the source: %v", err)
	}
	line := `{"type":"user","gitBranch":"b","pad":"` + strings.Repeat("a", 2*1024*1024) + `"}`
	byBranch := map[string]*BranchTranscriptStats{}
	if _, err := ReadTranscriptStream(context.Background(), "wide", strings.NewReader(line+"\n"), NewClassifier(nil), byBranch); err != nil || byBranch["b"] == nil {
		t.Fatalf("a 2 MiB line (over the old 1 MiB cap) must read: %v", err)
	}
}
