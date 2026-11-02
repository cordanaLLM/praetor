package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/prwait"
)

type prCLIReader struct{ requested []int }

func (r *prCLIReader) OwnOpen(context.Context, string) ([]int, error) { return []int{7}, nil }
func (r *prCLIReader) Read(_ context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	r.requested = append(r.requested, number)
	return forge.ReviewSnapshot{Number: number, State: "OPEN", ChecksTerminal: true, ChecksConclusion: "SUCCESS"}, nil
}

func TestPRWaitCLIFlagsAndJSON(t *testing.T) {
	var output bytes.Buffer
	reader := &prCLIReader{}
	err := runPRWithReader([]string{"wait", "--repo", "example/widget", "--pr", "7", "--pr", "8", "--timeout", "1s", "--interval", "10ms", "--json"}, reader, &output)
	var result prwait.Result
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || len(result.Items) != 2 || len(reader.requested) != 2 || reader.requested[1] != 8 {
		t.Fatalf("flags or JSON lost: %+v reader=%+v", result, reader)
	}
}

func TestPRWaitCLITextAndInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"wait", "--repo", "example/widget"},
		{"wait", "--repo", "example/widget", "--pr", "0"},
		{"wait", "--repo", "example/widget", "--pr", "invalid"},
		{"wait", "--repo", "example/widget", "--interval", "0s"},
		{"wait", "--repo", "example/widget", "unexpected"},
	} {
		var output bytes.Buffer
		err := runPRWithReader(args, &prCLIReader{}, &output)
		if len(args) == 3 {
			if err != nil || output.String() != "checks example/widget#7: SUCCESS\n" {
				t.Fatalf("text contract: %q %v", output.String(), err)
			}
			continue
		}
		var status exitStatusError
		if !errors.As(err, &status) || status.code != 2 {
			t.Fatalf("%q: %v", args, err)
		}
	}
}

func TestPRWaitRegisteredAndHelp(t *testing.T) {
	if commandTable()["pr"] == nil {
		t.Fatal("pr not registered")
	}
	err := runPRWithReader([]string{"wait", "--help"}, &prCLIReader{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "help requested") {
		t.Fatalf("help: %v", err)
	}
}
