package agenthook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agyFixture is one payload under testdata/agy/<sub>, replayed end to end through Run().
type agyFixture struct {
	Name    string          `json:"name"`
	Event   string          `json:"event"`
	Allow   bool            `json:"allow"`
	Payload json.RawMessage `json:"payload"`
}

// loadAgyFixtures reads every *.json file directly under testdata/agy/<sub> (docs today;
// a future recorded-<os> directory replays the same way once a live capture exists).
func loadAgyFixtures(t *testing.T, sub string) []agyFixture {
	t.Helper()
	dir := filepath.Join("testdata", "agy", sub)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []agyFixture
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var fixture agyFixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) == 0 {
		t.Fatalf("%s has no fixtures", dir)
	}
	return fixtures
}

// agyStdoutAllows reads one agy decision body and reports whether it lets the client
// proceed: for pre-tool that is decision "allow", for stop anything but "continue".
func agyStdoutAllows(t *testing.T, event string, response Response) bool {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Stdout, &body); err != nil {
		t.Fatalf("agy stdout is not JSON: %q (%v)", response.Stdout, err)
	}
	decision, _ := body["decision"].(string)
	if event == "stop" {
		return decision != "continue"
	}
	return decision == "allow"
}

// TestRunReplaysTheDocsConfirmedAgyPayloads is the H3 positive case (rollout spec 9.3):
// every fixture under testdata/agy/docs is verified against the hooks.json contract
// embedded in the installed Antigravity 1.2.6 binary, not recorded from a live session
// (.workingdir/planning/wrap-fork-rollout-spec-20260917.md:854 and decision row 7).
func TestRunReplaysTheDocsConfirmedAgyPayloads(t *testing.T) {
	root := repository(t, true)
	for _, fixture := range loadAgyFixtures(t, "docs") {
		response := serve(t, "agy", fixture.Event, root, fixture.Payload)
		if response.ExitCode != 0 {
			t.Errorf("%s: exit %d: %+v", fixture.Name, response.ExitCode, response)
			continue
		}
		if agyStdoutAllows(t, fixture.Event, response) != fixture.Allow {
			t.Errorf("%s: allow=%v got %+v", fixture.Name, fixture.Allow, response)
		}
	}
}

func TestRunAgyPreToolGoldens(t *testing.T) {
	root := repository(t, true)
	allow := serve(t, "agy", "pre-tool", root, commandPayload(t, map[string]any{
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "git status"}},
	}))
	if allow.ExitCode != 0 || strings.TrimSpace(string(allow.Stdout)) != `{"decision":"allow"}` || len(allow.Stderr) != 0 {
		t.Errorf("agy allow golden: %+v", allow)
	}
	deny := serve(t, "agy", "pre-tool", root, commandPayload(t, map[string]any{
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "git commit --no-verify"}},
	}))
	if deny.ExitCode != 0 || !strings.Contains(string(deny.Stdout), `"decision":"deny"`) {
		t.Errorf("agy deny golden: %+v", deny)
	}
	invalid := serve(t, "agy", "pre-tool", root, []byte("not json"))
	if invalid.ExitCode != 0 || !strings.Contains(string(invalid.Stdout), `"decision":"deny"`) {
		t.Errorf("agy invalid input golden: %+v", invalid)
	}
}

func TestRunAgyStopGolden(t *testing.T) {
	root := repository(t, true)
	stop := serve(t, "agy", "stop", root, commandPayload(t, map[string]any{
		"executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true,
	}))
	if stop.ExitCode != 0 || strings.TrimSpace(string(stop.Stdout)) != `{}` {
		t.Errorf("agy stop golden: %+v", stop)
	}
}

func TestRunAgyEmptyWorkspacePathsFallsBackToTheProcessDirectory(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "agy", "pre-tool", root, commandPayload(t, map[string]any{
		"workspacePaths": []string{},
		"toolCall":       map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "git status"}},
	}))
	if response.ExitCode != 0 || strings.TrimSpace(string(response.Stdout)) != `{"decision":"allow"}` {
		t.Errorf("empty workspacePaths: %+v", response)
	}
}

func TestRunAgyUnclassifiedToolAllows(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "agy", "pre-tool", root, commandPayload(t, map[string]any{
		"toolCall": map[string]any{"name": "view_file", "args": map[string]any{}},
	}))
	if response.ExitCode != 0 || strings.TrimSpace(string(response.Stdout)) != `{"decision":"allow"}` {
		t.Errorf("unclassified tool: %+v", response)
	}
}

func TestRunAgyCommandToolWithoutACommandKeyDenies(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "agy", "pre-tool", root, commandPayload(t, map[string]any{
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{}},
	}))
	if response.ExitCode != 0 || !strings.Contains(string(response.Stdout), `"decision":"deny"`) {
		t.Errorf("command tool without a command key: %+v", response)
	}
}

// TestRunAgyUnknownEventIsRejected covers the H3 negative case: post-tool is a real,
// spec-declared event (section 3.1) with no registration row for agy yet (H2/H4), so it
// must fail the same usage path as any other unsupported client/event pair.
func TestRunAgyUnknownEventIsRejected(t *testing.T) {
	root := repository(t, true)
	response := serve(t, "agy", "post-tool", root, []byte(`{}`))
	if response.ExitCode != usageExit || !strings.Contains(string(response.Stderr), "usage: praetorctl hook <client> <event>") {
		t.Errorf("agy post-tool: %+v", response)
	}
}

func TestRunAgySkipsUngovernedWorkspacesWithAReason(t *testing.T) {
	plain := repository(t, false)
	response := serve(t, "agy", "pre-tool", plain, commandPayload(t, map[string]any{
		"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "rm -rf .git/hooks"}},
	}))
	if response.ExitCode != 0 || strings.TrimSpace(string(response.Stdout)) != `{"decision":"allow"}` ||
		string(response.Stderr) != "praetor hook: workspace not governed, skipped\n" {
		t.Errorf("agy ungoverned: %+v", response)
	}
}
