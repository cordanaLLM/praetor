package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/prwait"
)

type prCLIReader struct{ requested []int }

// OwnOpen returns a mock list of open pull request numbers for test runs.
func (r *prCLIReader) OwnOpen(context.Context, string) ([]int, error) { return []int{7}, nil }

// ReadBatch rejects stop-only batch reads through the pr-wait CLI fixture.
func (r *prCLIReader) ReadBatch(context.Context, string, []int) (map[int]forge.ReviewSnapshot, error) {
	return nil, errors.New("pr wait must use complete per-PR Read")
}

// Read records the requested pull request number and returns a mock review snapshot.
func (r *prCLIReader) Read(_ context.Context, _ string, number int) (forge.ReviewSnapshot, error) {
	r.requested = append(r.requested, number)
	return forge.ReviewSnapshot{Number: number, State: "OPEN", ChecksTerminal: true, ChecksConclusion: "SUCCESS"}, nil
}

// TestPRWaitCLIFlagsAndJSON pins flag parsing and JSON output serialization for the pr wait command.
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

// TestPRWaitCLITextAndInvalidFlags pins text output formatting and argument validation for invalid CLI invocations.
func TestPRWaitCLITextAndInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"wait", "--repo", "example/widget"},
		{},
		{"status"},
		{"status", "--json"},
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
		if !errors.As(err, &status) || status.code != 2 || !strings.Contains(output.String(), "pr") {
			t.Fatalf("%q: err=%v out=%q", args, err, output.String())
		}
	}
}

// TestPRWaitRegisteredAndHelp pins pr subcommand registration in the command table and help output.
func TestPRWaitRegisteredAndHelp(t *testing.T) {
	if commandTable()["pr"] == nil {
		t.Fatal("pr not registered")
	}
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}, {"wait", "--help"}} {
		var output bytes.Buffer
		err := runPRWithReader(args, &prCLIReader{}, &output)
		if !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("help %q: %v", args, err)
		}
	}
}

// TestPRWaitCLIFlagErrorJSONOutput pins error reporting in JSON or plain text depending on CLI flags.
func TestPRWaitCLIFlagErrorJSONOutput(t *testing.T) {
	// Positive: invalid flags before --json or --json=true emit JSON error
	assertPRWaitJSONError(t, []string{"wait", "--repo", "example/widget", "--bad", "value", "--json"}, "-bad")
	assertPRWaitJSONError(t, []string{"wait", "--bad", "value", "--json=true"}, "-bad")
	assertPRWaitJSONError(t, []string{"wait", "--repo", "example/widget", "--interval", "notaduration", "--json"}, "-interval")
	assertPRWaitJSONError(t, []string{"wait", "--repo", "example/widget", "--pr", "notanumber", "--json"}, "parse --pr")
	assertPRWaitJSONError(t, []string{"wait", "--interval", "--", "--json"}, "-interval")
	assertPRWaitJSONError(t, []string{"wait", "--timeout", "--", "--json"}, "-timeout")
	assertPRWaitJSONError(t, []string{"wait", "--pr", "--", "--json"}, "parse --pr")

	// Negative and boundary: explicit --json=false, terminator or missing flag emit text
	assertPRWaitTextError(t, []string{"wait", "--repo", "example/widget", "--bad", "value", "--json=false"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--repo", "example/widget", "--bad", "value"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--repo", "example/widget", "--bad", "value", "--", "--json"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--repo", "example/widget", "--bad", "value", "--json", "--json=false"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--repo", "--json", "--bad"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--repo", "--json=true", "--bad"}, "-bad")
	assertPRWaitTextError(t, []string{"wait", "--interval", "--", "--json=false"}, "-interval")
}

// assertPRWaitJSONError verifies that an invalid invocation emits the expected error message in JSON format.
func assertPRWaitJSONError(t *testing.T, args []string, needle string) {
	t.Helper()
	var out bytes.Buffer
	err := runPRWithReader(args, &prCLIReader{}, &out)
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 2 {
		t.Fatalf("expected exit code 2, got err=%v", err)
	}
	var res prwait.Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("expected JSON output, got %q: %v", out.String(), err)
	}
	if res.Code != 2 || len(res.Items) == 0 || !strings.Contains(res.Items[0], needle) {
		t.Fatalf("unexpected JSON payload: %+v", res)
	}
}

// assertPRWaitTextError verifies that an invalid invocation emits the expected error message in plain text.
func assertPRWaitTextError(t *testing.T, args []string, needle string) {
	t.Helper()
	var out bytes.Buffer
	err := runPRWithReader(args, &prCLIReader{}, &out)
	var status exitStatusError
	if !errors.As(err, &status) || status.code != 2 {
		t.Fatalf("expected exit code 2, got err=%v", err)
	}
	var res prwait.Result
	if err := json.Unmarshal(out.Bytes(), &res); err == nil && res.Code != 0 {
		t.Fatalf("expected plain text output, got JSON: %s", out.String())
	}
	if !strings.Contains(out.String(), needle) {
		t.Fatalf("expected text output containing %q, got: %s", needle, out.String())
	}
}
