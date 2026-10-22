package agenthook

import (
	"context"
	"strings"
	"testing"
)

func TestCheckpointSessionPreparationValidatesHashAndFailsClosed(t *testing.T) {
	root := repository(t, true)
	_, getenv := buildStub(t, "python3")
	argv, err := ResolveInterpreter(getenv, [][]string{{"python3"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, output string
		valid        bool
	}{
		{"hash", "PRAETOR_CHECKPOINT_SESSION=0123456789abcdef\n", true},
		{"raw id", "PRAETOR_CHECKPOINT_SESSION=private-session\n", false},
		{"uppercase", "PRAETOR_CHECKPOINT_SESSION=0123456789ABCDEF\n", false},
		{"missing", cleanReport, false},
		{"duplicate", strings.Repeat("PRAETOR_CHECKPOINT_SESSION=0123456789abcdef\n", 2), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubStdout(t, tc.output)
			_, err := checkpointSessionContext(t.Context(), argv, root, "stop", Canonical{ConversationID: "private-session"})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestCheckpointSessionAbsentDoesNotInvokePreparation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if _, err := checkpointSessionContext(ctx, nil, t.TempDir(), "stop", Canonical{}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointSessionDecodeNativeOptionalID(t *testing.T) {
	for _, client := range []string{"claude", "codex", "gemini"} {
		d, _ := DialectFor(client)
		for _, event := range []Event{EventPostTool, EventStop} {
			for _, tc := range []struct{ field, want string }{
				{`,"session_id":"session"`, "session"},
				{"", ""}, {`,"session_id":null`, ""},
				{`,"session_id":42`, ""}, {`,"session_id":{}`, ""},
				{`,"session_id":""`, ""},
			} {
				payload := `{"tool_name":"Edit","tool_input":{"file_path":"/w/file"}` + tc.field + `}`
				got, err := d.Decode(event, []byte(payload))
				if err != nil || got.ConversationID != tc.want {
					t.Fatalf("%s %s %s: %+v %v", client, event, tc.field, got, err)
				}
				wantInput := event == EventPostTool && tc.want != ""
				if (len(got.CheckpointInput) > 0) != wantInput {
					t.Fatalf("%s %s %s: unexpected tool operands", client, event, tc.field)
				}
			}
		}
	}
}
