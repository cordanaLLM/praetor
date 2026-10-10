package agenthook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/codexhook"
)

// codexEventFixtures are the complete Codex hook payloads of the events the registration table
// serves for codex. tools/schemacheck validates the same files against the published schema, so
// a field Codex requires cannot be missing from them; these tests prove the generated types and
// Praetor's decoder both accept them.
var codexEventFixtures = []struct {
	file  string
	event Event
	typed any
}{
	{"pre-tool.json", EventPreTool, &codexhook.PreToolUseCommandInput{}},
	{"post-tool.json", EventPostTool, &codexhook.PostToolUseCommandInput{}},
	{"stop.json", EventStop, &codexhook.StopCommandInput{}},
	{"subagent-stop.json", EventPostReturn, &codexhook.SubagentStopCommandInput{}},
}

func readCodexEvent(t *testing.T, file, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "codex-events", file))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, []byte(repositoryPlaceholder), []byte(strings.ReplaceAll(root, `\`, `\\`)))
}

func TestCodexEventFixturesDecodeIntoTheGeneratedTypes(t *testing.T) {
	for _, fixture := range codexEventFixtures {
		t.Run(fixture.file, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(readCodexEvent(t, fixture.file, t.TempDir())))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(fixture.typed); err != nil {
				t.Fatalf("generated type refuses the fixture: %v", err)
			}
		})
	}
}

func TestCodexEventFixturesDecodeInThePraetorDialect(t *testing.T) {
	dialect, ok := DialectFor("codex")
	if !ok {
		t.Fatal("no codex dialect")
	}
	for _, fixture := range codexEventFixtures {
		t.Run(fixture.file, func(t *testing.T) {
			canonical, err := dialect.Decode(fixture.event, readCodexEvent(t, fixture.file, t.TempDir()))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if canonical.Event != fixture.event {
				t.Errorf("event %q, want %q", canonical.Event, fixture.event)
			}
		})
	}
}

// TestCodexEventFixtureForTheWrongEventIsRefused is the negative case: a Stop payload offered as
// a pre-tool event contradicts the event name.
func TestCodexEventFixtureForTheWrongEventIsRefused(t *testing.T) {
	dialect, _ := DialectFor("codex")
	if _, err := dialect.Decode(EventPreTool, readCodexEvent(t, "stop.json", t.TempDir())); err == nil {
		t.Fatal("a Stop payload decoded as a pre-tool event")
	}
}

// TestGeneratedTypesRefuseAnExtraMember is the boundary case of the strict decoding above: the
// published input schemas forbid additional properties, and the fixtures must keep to them.
func TestGeneratedTypesRefuseAnExtraMember(t *testing.T) {
	data := readCodexEvent(t, "stop.json", t.TempDir())
	data = bytes.Replace(data, []byte(`"stop_hook_active"`), []byte(`"surprise": 1, "stop_hook_active"`), 1)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&codexhook.StopCommandInput{}); err == nil {
		t.Fatal("unknown member accepted")
	}
}
