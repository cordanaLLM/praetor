// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

var ledgerPRs = []forge.MergedPullRequest{
	{Number: 1, HeadBranch: "feat/a", MergedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
	{Number: 2, HeadBranch: "feat/b", MergedAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)},
}

// Rows use the columns of LiteLLM_SpendLogs: request_id, model, model_group, spend,
// total_tokens, prompt_tokens, completion_tokens, startTime, request_tags and metadata.
const spendJSONL = `{"request_id":"r1","call_type":"acompletion","model":"claude-sonnet-4-5","model_group":"","spend":0.5,"total_tokens":1000,"prompt_tokens":900,"completion_tokens":100,"startTime":"2026-10-01T10:00:00Z","request_tags":["branch:feat/a"],"metadata":{}}
{"request_id":"r2","model":"qwen-local","model_group":"cordana/coding","spend":0,"total_tokens":0,"prompt_tokens":100,"completion_tokens":50,"startTime":"2026-10-01T10:01:00Z","request_tags":[],"metadata":{"tags":["feat/a"]}}
{"request_id":"r3","model":"ollama/llama3","model_group":"local","spend":0,"total_tokens":300,"startTime":"2026-10-01T10:02:00Z","request_tags":["pr:2"],"metadata":null}
{"request_id":"r4","model":"gpt-5-mini","spend":0.02,"total_tokens":50,"request_tags":null,"metadata":"{\"branch\":\"feat/b\"}"}
{"request_id":"r5","model":"claude-opus-4-1","spend":0.25,"total_tokens":10,"request_tags":["team:x"],"metadata":{"user_api_key":"h"}}
{"request_id":"r1","model":"claude-sonnet-4-5","spend":0.5,"total_tokens":1000,"request_tags":["branch:feat/a"]}
`

func assertSpendReport(t *testing.T, r *SpendReport) {
	t.Helper()
	if r.Entries != 5 || r.DuplicateRequests != 1 {
		t.Errorf("entries %d duplicates %d, want 5 and 1 (r1 repeats)", r.Entries, r.DuplicateRequests)
	}
	if r.RequestsByPRNumber[1] != 2 || r.RequestsByPRNumber[2] != 2 {
		t.Errorf("requests: %v", r.RequestsByPRNumber)
	}
	assertSpendAmounts(t, r)
	assertSpendClasses(t, r)
}

func assertSpendAmounts(t *testing.T, r *SpendReport) {
	t.Helper()
	if r.SpendByPRNumber[1] != 0.5 || r.SpendByPRNumber[2] != 0.02 {
		t.Errorf("spend: %v", r.SpendByPRNumber)
	}
	if r.Unattributed != 0.25 || r.TotalSpend < 0.7699 || r.TotalSpend > 0.7701 {
		t.Errorf("unattributed %v total %v", r.Unattributed, r.TotalSpend)
	}
}

func assertSpendClasses(t *testing.T, r *SpendReport) {
	t.Helper()
	// r1 sonnet 1000 + r2 router class coding with prompt+completion 150; r3/r4 are local or cheap.
	if r.FrontierTokensByPRNumber[1] != 1150 || r.FrontierTokensByPRNumber[2] != 0 {
		t.Errorf("frontier tokens: %v", r.FrontierTokensByPRNumber)
	}
	// r3 is zero spend and local, and still counts; r2 (class coding) is not local.
	if r.LocalRequestsByPRNumber[2] != 1 || r.LocalRequestsByPRNumber[1] != 0 {
		t.Errorf("local requests: %v", r.LocalRequestsByPRNumber)
	}
}

func TestReadSpendLogFile_Positive_LiteLLMFieldsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spend.jsonl")
	writeFile(t, path, []byte(spendJSONL))
	r, err := ReadSpendLogFile(context.Background(), path, ledgerPRs, NewClassifier(nil))
	if err != nil {
		t.Fatal(err)
	}
	assertSpendReport(t, r)
}

func TestReadSpendLogFile_Positive_ArrayAndCSVAgree(t *testing.T) {
	dir := t.TempDir()
	rows := strings.Split(strings.TrimSpace(spendJSONL), "\n")
	writeFile(t, filepath.Join(dir, "spend.json"), []byte("["+strings.Join(rows, ",")+"]"))
	csv := "request_id,model,model_group,spend,total_tokens,prompt_tokens,completion_tokens,startTime,request_tags,metadata\n" +
		`r1,claude-sonnet-4-5,,0.5,1000,900,100,2026-10-01T10:00:00Z,"[""branch:feat/a""]",{}` + "\n" +
		`r2,qwen-local,cordana/coding,0,0,100,50,2026-10-01T10:01:00Z,[],"{""tags"": [""feat/a""]}"` + "\n" +
		`r3,ollama/llama3,local,0,300,,,2026-10-01T10:02:00Z,"[""pr:2""]",` + "\n" +
		`r4,gpt-5-mini,,0.02,50,,,,,"{""branch"": ""feat/b""}"` + "\n" +
		`r5,claude-opus-4-1,,0.25,10,,,,"[""team:x""]",` + "\n" +
		`r1,claude-sonnet-4-5,,0.5,1000,,,,"[""branch:feat/a""]",` + "\n"
	writeFile(t, filepath.Join(dir, "spend.csv"), []byte(csv))
	for _, name := range []string{"spend.json", "spend.csv"} {
		r, err := ReadSpendLogFile(context.Background(), filepath.Join(dir, name), ledgerPRs, NewClassifier(nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertSpendReport(t, r)
	}
}

func TestReadSpendLogFile_Positive_FormatSniffedWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lines"), []byte(spendJSONL))
	writeFile(t, filepath.Join(dir, "array"), []byte(`[`+strings.ReplaceAll(strings.TrimSpace(spendJSONL), "\n", ",")+`]`))
	for _, name := range []string{"lines", "array"} {
		r, err := ReadSpendLogFile(context.Background(), filepath.Join(dir, name), ledgerPRs, NewClassifier(nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		assertSpendReport(t, r)
	}
}

func TestReadSpendLogFile_Negative(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"bad.jsonl":     "{\"request_id\":\"a\"}\nnot json\n",
		"bad.json":      `[{"request_id":"a"},`,
		"nohead.csv":    "foo,bar\n1,2\n",
		"badcell.jsonl": `{"spend":"x"}` + "\n",
	}
	for name, body := range cases {
		writeFile(t, filepath.Join(dir, name), []byte(body))
		if _, err := ReadSpendLogFile(context.Background(), filepath.Join(dir, name), nil, NewClassifier(nil)); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	if _, err := ReadSpendLogFile(context.Background(), filepath.Join(dir, "missing.jsonl"), nil, NewClassifier(nil)); err == nil {
		t.Error("missing file must fail")
	}
	if _, err := ReadSpendLogFile(context.Background(), "  ", nil, NewClassifier(nil)); err == nil {
		t.Error("empty path must fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadSpendLogFile(ctx, filepath.Join(dir, "bad.jsonl"), nil, NewClassifier(nil)); err == nil {
		t.Error("cancelled context must fail")
	}
}

func TestReadSpendLogFile_Boundary_CapsFailTheRead(t *testing.T) {
	dir := t.TempDir()
	row := `{"request_id":"%d","spend":1}`
	var jsonl, array, csv strings.Builder
	csv.WriteString("request_id,spend\n")
	array.WriteString("[")
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&jsonl, row+"\n", i)
		if i > 0 {
			array.WriteString(",")
		}
		fmt.Fprintf(&array, row, i)
		fmt.Fprintf(&csv, "%d,1\n", i)
	}
	array.WriteString("]")
	files := map[string]string{"s.jsonl": jsonl.String(), "s.json": array.String(), "s.csv": csv.String()}
	for name, body := range files {
		path := filepath.Join(dir, name)
		writeFile(t, path, []byte(body))
		c := NewClassifier(nil)
		if _, err := readSpendLogFile(context.Background(), path, nil, c, sourceLimits{Files: 1, Lines: 3, Bytes: 1 << 20}); err != nil {
			t.Errorf("%s at the line cap must read: %v", name, err)
		}
		if _, err := readSpendLogFile(context.Background(), path, nil, c, sourceLimits{Files: 1, Lines: 2, Bytes: 1 << 20}); err == nil || !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "bound") {
			t.Errorf("%s over the line cap must fail naming the cap: %v", name, err)
		}
		if _, err := readSpendLogFile(context.Background(), path, nil, c, sourceLimits{Files: 1, Lines: 10, Bytes: 20}); err == nil {
			t.Errorf("%s over the byte cap must fail", name)
		}
	}
}

func TestAttribution_Boundary_ReusedBranchBelongsToLatestMergedPR(t *testing.T) {
	prs := []forge.MergedPullRequest{
		{Number: 5, HeadBranch: "fix/x", MergedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
		{Number: 9, HeadBranch: "fix/x", MergedAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)},
	}
	if got := branchOwners(prs)["fix/x"]; got != 9 {
		t.Fatalf("owner = %d, want 9", got)
	}
}
