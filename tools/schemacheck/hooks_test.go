package schemacheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agenthook"
)

// codexInputSchemas maps a Codex hook event name to its vendored input schema.
var codexInputSchemas = map[string]string{
	"PreToolUse":   "codex/hooks/pre-tool-use.command.input.schema.json",
	"PostToolUse":  "codex/hooks/post-tool-use.command.input.schema.json",
	"Stop":         "codex/hooks/stop.command.input.schema.json",
	"SubagentStop": "codex/hooks/subagent-stop.command.input.schema.json",
}

func hookFixture(t *testing.T, rel string) []byte {
	t.Helper()
	data := readRepoFile(t, "internal/agenthook/testdata/"+rel)
	return []byte(strings.ReplaceAll(string(data), "{repository}", "/work/repo"))
}

// codexPayloads returns every Codex hook payload fixture: the complete per-event files and the
// payloads of the brief fixtures.
func codexPayloads(t *testing.T) map[string][]byte {
	t.Helper()
	payloads := map[string][]byte{}
	files, err := filepath.Glob(filepath.Join(repoRoot, "internal", "agenthook", "testdata", "codex-events", "*.json"))
	if err != nil || len(files) != 4 {
		t.Fatalf("codex-events fixtures: %v, %d files", err, len(files))
	}
	for _, file := range files {
		payloads["codex-events/"+filepath.Base(file)] = hookFixture(t, "codex-events/"+filepath.Base(file))
	}
	var cases []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(hookFixture(t, "agent-text/codex/cases.json"), &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if len(c.Payload) > 0 {
			payloads["agent-text/codex/cases.json#"+c.Name] = c.Payload
		}
	}
	return payloads
}

func eventOf(t *testing.T, payload []byte) string {
	t.Helper()
	var head struct {
		Event string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		t.Fatal(err)
	}
	return head.Event
}

func TestCodexHookFixturesValidateAgainstThePublishedInputSchemas(t *testing.T) {
	for name, payload := range codexPayloads(t) {
		t.Run(name, func(t *testing.T) {
			event := eventOf(t, payload)
			relPath, ok := codexInputSchemas[event]
			if !ok {
				t.Fatalf("no vendored input schema for event %q", event)
			}
			if err := compileVendored(t, relPath).Validate(payload); err != nil {
				t.Fatalf("%s violates %s: %v", name, relPath, err)
			}
		})
	}
}

// TestEveryRegisteredCodexEventHasASchemaCheckedFixture ties the registration table to the
// fixtures: a codex row without a complete payload fixture fails.
func TestEveryRegisteredCodexEventHasASchemaCheckedFixture(t *testing.T) {
	covered := map[string]bool{}
	for _, payload := range codexPayloads(t) {
		covered[eventOf(t, payload)] = true
	}
	for _, row := range agenthook.Registrations("codex") {
		if !covered[row.NativeEvent] {
			t.Errorf("codex %s (%s) has no schema-checked payload fixture", row.NativeEvent, row.Event)
		}
		if _, ok := codexInputSchemas[row.NativeEvent]; !ok {
			t.Errorf("codex %s has no vendored input schema", row.NativeEvent)
		}
	}
}

func TestCodexHookFixtureMutationsFail(t *testing.T) {
	base := hookFixture(t, "codex-events/stop.json")
	schema := compileVendored(t, codexInputSchemas["Stop"])
	mutations := map[string]struct {
		edit  func(object map[string]any)
		field string
	}{
		"missing required":  {func(o map[string]any) { delete(o, "turn_id") }, "turn_id"},
		"extra member":      {func(o map[string]any) { o["surprise"] = 1 }, "surprise"},
		"wrong event":       {func(o map[string]any) { o["hook_event_name"] = "Stopped" }, "hook_event_name"},
		"wrong type":        {func(o map[string]any) { o["stop_hook_active"] = "yes" }, "stop_hook_active"},
		"renamed on server": {func(o map[string]any) { o["turnId"] = o["turn_id"]; delete(o, "turn_id") }, "turn_id"},
	}
	for name, m := range mutations {
		var object map[string]any
		if err := json.Unmarshal(base, &object); err != nil {
			t.Fatal(err)
		}
		m.edit(object)
		mutated, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		err = schema.Validate(mutated)
		var violations *Violations
		if !errors.As(err, &violations) || !violations.Names(m.field) {
			t.Errorf("%s: Validate = %v, want a violation naming %s", name, err, m.field)
		}
	}
	if err := schema.Validate(base); err != nil {
		t.Errorf("unmutated fixture refused: %v", err)
	}
}

// TestUnvalidatedFixtureFamiliesAreListedInTheGuide keeps the list of fixture families without
// an upstream schema in the guide in step with the directories on disk.
func TestUnvalidatedFixtureFamiliesAreListedInTheGuide(t *testing.T) {
	guide := string(readRepoFile(t, "docs/guides/client-schemas.md"))
	entries, err := os.ReadDir(filepath.Join(repoRoot, "internal", "agenthook", "testdata", "agent-text"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "codex" && !strings.Contains(guide, "agent-text/"+entry.Name()) {
			t.Errorf("docs/guides/client-schemas.md does not list the %s fixtures as unvalidated", entry.Name())
		}
	}
	for _, name := range []string{"agy/docs", "pre-tool/cases.json"} {
		if !strings.Contains(guide, name) {
			t.Errorf("docs/guides/client-schemas.md does not list %s", name)
		}
	}
	if !slices.Contains(strings.Fields(guide), "Copilot") {
		t.Error("the guide does not name Copilot among the formats without a schema")
	}
}
