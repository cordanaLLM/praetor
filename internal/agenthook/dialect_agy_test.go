package agenthook

import (
	"reflect"
	"strings"
	"testing"
)

func TestAgyIsRegistered(t *testing.T) {
	dialect, ok := DialectFor("agy")
	if !ok || dialect.Client != "agy" {
		t.Fatalf("agy has no dialect: %+v", dialect)
	}
}

func TestAgyDecodePreTool(t *testing.T) {
	agy, _ := DialectFor("agy")
	// The docs' own PreToolUse example (docs/guides/agent-hooks.md, agy contract).
	payload := commandPayload(t, map[string]any{
		"toolCall":       map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "npm test"}},
		"stepIdx":        19,
		"conversationId": "ec33ebf9-0cba-4100-8142-c61503f6c587",
		"workspacePaths": []string{"/path/to/workspace"},
	})
	got, err := agy.Decode(EventPreTool, payload)
	want := Canonical{
		Event: EventPreTool, Tool: "run_command", Command: "npm test",
		Workspaces: []string{"/path/to/workspace"}, ConversationID: "ec33ebf9-0cba-4100-8142-c61503f6c587", Step: 19,
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("decode: %+v %v", got, err)
	}
}

func TestAgyDecodeNoToolCallAllows(t *testing.T) {
	agy, _ := DialectFor("agy")
	got, err := agy.Decode(EventPreTool, []byte(`{"stepIdx":1}`))
	if err != nil || got.Command != "" || got.Tool != "" {
		t.Fatalf("no toolCall: %+v %v", got, err)
	}
}

func TestAgyDecodeUnclassifiedToolAllows(t *testing.T) {
	agy, _ := DialectFor("agy")
	payload := commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "view_file", "args": map[string]any{}}})
	got, err := agy.Decode(EventPreTool, payload)
	if err != nil || got.Command != "" || got.Tool != "view_file" {
		t.Fatalf("unclassified tool: %+v %v", got, err)
	}
}

func TestAgyDecodeEmptyToolNameIsNotACommand(t *testing.T) {
	// The generic Dialect.Decode treats an absent tool name as its registration's one
	// matched tool (claude/codex/gemini match a single tool). agy's registration matches
	// every tool ("*"), so an empty name must stay unclassified, not become run_command.
	agy, _ := DialectFor("agy")
	got, err := agy.Decode(EventPreTool, commandPayload(t, map[string]any{"toolCall": map[string]any{"name": ""}}))
	if err != nil || got.Command != "" {
		t.Fatalf("empty tool name treated as a command: %+v %v", got, err)
	}
}

func TestAgyDecodeEmptyWorkspacePaths(t *testing.T) {
	agy, _ := DialectFor("agy")
	payload := commandPayload(t, map[string]any{"workspacePaths": []string{}, "toolCall": map[string]any{"name": "view_file"}})
	got, err := agy.Decode(EventPreTool, payload)
	if err != nil || got.Workspaces != nil {
		t.Fatalf("empty workspacePaths: %+v %v", got, err)
	}
}

func TestAgyDecodeCommandToolWithoutACommandKey(t *testing.T) {
	agy, _ := DialectFor("agy")
	for name, payload := range map[string][]byte{
		"no args":       commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "run_command"}}),
		"empty args":    commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{}}}),
		"wrong key":     commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"command": "git status"}}}),
		"blank command": commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": "   "}}}),
		"null args":     commandPayload(t, map[string]any{"toolCall": map[string]any{"name": "run_command", "args": nil}}),
	} {
		if decoded, err := agy.Decode(EventPreTool, payload); err == nil {
			t.Errorf("%s decoded to %+v", name, decoded)
		}
	}
}

func TestAgyDecodeWrongTypes(t *testing.T) {
	agy, _ := DialectFor("agy")
	for name, payload := range map[string]string{
		"toolCall.name is a number":  `{"toolCall":{"name":7}}`,
		"toolCall is a string":       `{"toolCall":"run_command"}`,
		"workspacePaths is a string": `{"workspacePaths":"not-an-array"}`,
		"conversationId is a number": `{"conversationId":7}`,
		"stepIdx is a string":        `{"stepIdx":"19"}`,
		"args is a string":           `{"toolCall":{"name":"run_command","args":"CommandLine=x"}}`,
	} {
		if decoded, err := agy.Decode(EventPreTool, []byte(payload)); err == nil {
			t.Errorf("%s decoded to %+v", name, decoded)
		}
	}
	if decoded, err := agy.Decode(EventStop, []byte(`{"executionNum":"1"}`)); err == nil {
		t.Errorf("stop executionNum wrong type decoded to %+v", decoded)
	}
}

func TestAgyDecodeRawBoundaries(t *testing.T) {
	agy, _ := DialectFor("agy")
	for _, raw := range rawCases() {
		if _, err := agy.Decode(EventPreTool, raw.payload); (err == nil) != raw.allow {
			t.Errorf("%s: allow=%v", raw.name, raw.allow)
		}
	}
}

func TestAgyDecodeStopTracksTheSecondStopInOneConversation(t *testing.T) {
	agy, _ := DialectFor("agy")
	first, err := agy.Decode(EventStop, commandPayload(t, map[string]any{
		"executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true, "conversationId": "c-1",
	}))
	if err != nil || first.StopActive {
		t.Fatalf("first stop: %+v %v", first, err)
	}
	second, err := agy.Decode(EventStop, commandPayload(t, map[string]any{
		"executionNum": 2, "terminationReason": "model_stop", "fullyIdle": true, "conversationId": "c-1",
	}))
	if err != nil || !second.StopActive {
		t.Fatalf("second stop: %+v %v", second, err)
	}
	if first.ConversationID != "c-1" || second.ConversationID != "c-1" {
		t.Fatalf("conversation id lost: %+v %+v", first, second)
	}
	noCounter, err := agy.Decode(EventStop, commandPayload(t, map[string]any{"terminationReason": "model_stop"}))
	if err != nil || noCounter.StopActive {
		t.Fatalf("stop without executionNum: %+v %v", noCounter, err)
	}
}

func TestAgyDecodeUnknownEvent(t *testing.T) {
	agy, _ := DialectFor("agy")
	if _, err := agy.Decode(Event("post-tool"), []byte(`{}`)); err == nil {
		t.Error("agy decoded an event it has no shape for")
	}
}

func TestAgyEncodePreTool(t *testing.T) {
	agy, _ := DialectFor("agy")
	for _, tc := range []struct {
		name    string
		verdict Verdict
		want    string
	}{
		{"allow", Verdict{Outcome: Allow}, `{"decision":"allow"}`},
		{"deny", Verdict{Deny, "no"}, `{"decision":"deny","reason":"no"}`},
		{"skip answers allow", Verdict{Skip, "workspace not governed"}, `{"decision":"allow"}`},
		{"unknown outcome fails closed", Verdict{Outcome: Outcome(9), Reason: "?"}, `{"decision":"deny","reason":"?"}`},
	} {
		got := agy.Encode(Canonical{Event: EventPreTool}, tc.verdict)
		if got.ExitCode != 0 || strings.TrimSpace(string(got.Stdout)) != tc.want {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	skip := agy.Encode(Canonical{Event: EventPreTool}, Verdict{Skip, "workspace not governed"})
	if string(skip.Stderr) != "praetor hook: workspace not governed, skipped\n" {
		t.Errorf("skip stderr: %q", skip.Stderr)
	}
}

func TestAgyEncodeStopBlocksOnceThenLetsGo(t *testing.T) {
	agy, _ := DialectFor("agy")
	first := agy.Encode(Canonical{Event: EventStop, StopActive: false}, Verdict{Deny, "invalid hook input"})
	if strings.TrimSpace(string(first.Stdout)) != `{"decision":"continue","reason":"invalid hook input"}` {
		t.Errorf("first bad stop: %+v", first)
	}
	second := agy.Encode(Canonical{Event: EventStop, StopActive: true}, Verdict{Deny, "invalid hook input"})
	if strings.TrimSpace(string(second.Stdout)) != `{}` {
		t.Errorf("second bad stop still blocked: %+v", second)
	}
	allowed := agy.Encode(Canonical{Event: EventStop}, Verdict{Outcome: Allow})
	if strings.TrimSpace(string(allowed.Stdout)) != `{}` {
		t.Errorf("allowed stop: %+v", allowed)
	}
	skipped := agy.Encode(Canonical{Event: EventStop}, Verdict{Skip, "workspace not governed"})
	if strings.TrimSpace(string(skipped.Stdout)) != `{}` {
		t.Errorf("skipped stop is neutral, not blocked: %+v", skipped)
	}
}

func TestAgyEncodeHasNoShapeForOtherEvents(t *testing.T) {
	agy, _ := DialectFor("agy")
	got := agy.Encode(Canonical{Event: EventEnvironment}, Verdict{Outcome: Allow})
	if got.ExitCode == 0 {
		t.Errorf("agy encoded an event it has no shape for: %+v", got)
	}
}
