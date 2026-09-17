package agenthook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// organisationContainerPattern is the operator half of the Python topology rule with one
// generic container name. The Python guard carries it built in; the Go engine receives it
// through the operator deny list, so the replay supplies it explicitly.
const organisationContainerPattern = `(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*/dev/(scratch)/?(\s|$)`

// fixtureCase is one JSON-expressible payload of the Python suites.
type fixtureCase struct {
	Name     string          `json:"name"`
	Allow    bool            `json:"allow"`
	Operator bool            `json:"operator"`
	Payload  json.RawMessage `json:"payload"`
}

// rawCase is a payload JSON cannot express: bytes, truncation and the size boundary.
type rawCase struct {
	name    string
	payload []byte
	allow   bool
}

func loadCases(t *testing.T) []fixtureCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "pre-tool", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatalf("fixture file lost cases: %d", len(cases))
	}
	return cases
}

// rawCases mirrors test_guard_rejects_malformed_and_oversized_json and the adapter cases
// of test_codex_adapter_routes_guard_and_normalizes_failures.
func rawCases() []rawCase {
	valid := []byte(`{"tool_input":{"command":"git status"}}`)
	boundary := append(bytes.Clone(valid), bytes.Repeat([]byte(" "), MaxInputBytes-len(valid))...)
	nested := append(bytes.Repeat([]byte("["), 2000), bytes.Repeat([]byte("]"), 2000)...)
	return []rawCase{
		{"empty", nil, false},
		{"not-json", []byte("not json"), false},
		{"truncated", []byte(`{"tool_input":`), false},
		{"invalid-utf8", []byte{0xff}, false},
		{"invalid-utf8-inside-command", []byte("{\"tool_input\":{\"command\":\"git status \xff\"}}"), false},
		{"deeply-nested-array", nested, false},
		{"two-documents", append(bytes.Clone(valid), []byte(" {}")...), false},
		{"only-spaces-over-the-bound", bytes.Repeat([]byte(" "), MaxInputBytes+1), false},
		{"exactly-one-mebibyte", boundary, true},
		{"one-mebibyte-plus-one", append(bytes.Clone(boundary), ' '), false},
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; workspace resolution cannot run on this leg")
	}
}

// repository creates an empty Git repository, governed when it holds the manifest.
func repository(t *testing.T, governed bool) string {
	t.Helper()
	requireGit(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGitProbe(context.Background(), dir, 4096, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if governed {
		if err := os.WriteFile(filepath.Join(dir, manifestName), []byte("version: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func policy(t *testing.T, operatorDeny ...string) *Policy {
	t.Helper()
	compiled, err := NewPolicy(operatorDeny)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func noEnvironment(string) string { return "" }

// serve runs one call with a clean environment and the built-in policy.
func serve(t *testing.T, client, event, workDir string, payload []byte) Response {
	t.Helper()
	return Run(context.Background(), Invocation{
		Client: client, Event: event, Stdin: bytes.NewReader(payload),
		Getenv: noEnvironment, WorkDir: workDir, Policy: policy(t),
	})
}

func commandPayload(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
