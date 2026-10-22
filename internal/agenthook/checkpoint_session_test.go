package agenthook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func loadShippedCheckpointScript(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".config", "lefthook", "scripts", "checkpoint.py"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func seedCheckpointScript(t *testing.T, root, text string) {
	t.Helper()
	dir := filepath.Join(root, checkpointScriptDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.py"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointSessionPreparationValidatesHashAndFailsClosed(t *testing.T) {
	root := repository(t, true)
	seedCheckpointScript(t, root, loadShippedCheckpointScript(t))
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

func TestCheckpointSessionPreparation_PlantedOldEvaluator(t *testing.T) {
	shipped := loadShippedCheckpointScript(t)
	planted := strings.Replace(shipped, "    parser.add_argument(\"--prepare-session\", action=\"store_true\")\n", "", 1)
	if planted == shipped {
		t.Fatal("failed to remove --prepare-session definition")
	}

	root := repository(t, true)
	seedCheckpointScript(t, root, planted)

	dir := t.TempDir()
	path := testsupport.BuildExecutable(t, dir, "python3", `package main
import "os"
func main() { os.Exit(99) }
`)
	argv := []string{path}

	_, err := checkpointSessionContext(t.Context(), argv, root, "stop", Canonical{ConversationID: "session-123"})
	if err == nil {
		t.Fatal("planted evaluator without --prepare-session must fail")
	}
	errStr := err.Error()
	const wantCommand = "praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT"
	if !strings.Contains(errStr, wantCommand) {
		t.Fatalf("error %q must name refresh command %q", errStr, wantCommand)
	}
	if !strings.Contains(errStr, "checkpoint.py") {
		t.Fatalf("error %q must name evaluator file", errStr)
	}
	if strings.Contains(errStr, "unrecognized arguments") || strings.Contains(errStr, "exit status 99") {
		t.Fatalf("error %q must not contain unrecognized arguments or interpreter exit status; interpreter must not start", errStr)
	}
}

func TestCheckpointSessionPreparation_SecondBarrierUsageError(t *testing.T) {
	shipped := loadShippedCheckpointScript(t)
	root := repository(t, true)
	seedCheckpointScript(t, root, shipped)

	_, stubGetenv := buildStub(t, "python3")
	stubArgv, err := ResolveInterpreter(stubGetenv, [][]string{{"python3"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRAETOR_STUB_STDERR", "checkpoint.py: error: unrecognized arguments: --prepare-session\n")
	t.Setenv("PRAETOR_STUB_EXIT", "2")

	_, err = checkpointSessionContext(t.Context(), stubArgv, root, "stop", Canonical{ConversationID: "session-123"})
	if err == nil {
		t.Fatal("usage error must fail")
	}
	errStr := err.Error()
	const wantCommand = "praetorctl adopt --lock-source-root=REVIEWED_PRAETOR_CHECKOUT"
	if !strings.Contains(errStr, wantCommand) {
		t.Fatalf("error %q must name refresh command %q", errStr, wantCommand)
	}
	if !strings.Contains(errStr, "checkpoint.py") {
		t.Fatalf("error %q must name evaluator file", errStr)
	}
	if strings.Contains(errStr, "unrecognized arguments") {
		t.Fatalf("error %q must map unrecognized arguments to refresh command", errStr)
	}
}

func TestCheckpointSessionPreparation_CurrentEvaluatorFlowsHash(t *testing.T) {
	shipped := loadShippedCheckpointScript(t)
	root := repository(t, true)
	seedCheckpointScript(t, root, shipped)

	_, stubGetenv := buildStub(t, "python3")
	stubArgv, err := ResolveInterpreter(stubGetenv, [][]string{{"python3"}})
	if err != nil {
		t.Fatal(err)
	}
	stubStdout(t, "PRAETOR_CHECKPOINT_SESSION=0123456789abcdef\n")

	ctx, err := checkpointSessionContext(t.Context(), stubArgv, root, "stop", Canonical{ConversationID: "session-123"})
	if err != nil {
		t.Fatalf("current evaluator must succeed: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
}
